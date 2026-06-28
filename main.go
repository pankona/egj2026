package main

import (
	"bytes"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/examples/resources/fonts"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

const (
	ScreenWidth  = 640
	ScreenHeight = 480

	CellSize   = 8
	GridWidth  = ScreenWidth / CellSize  // 80
	GridHeight = ScreenHeight / CellSize // 60

	// Energy is quantized into MaxStock discrete units. A single drag spends
	// 1, 2, or 3 units depending on its length (mapped via the *DragMax
	// thresholds below) and the resulting beam length is *Length for that
	// unit count. This gives "small-dab burst spam" and "all-in big swipe"
	// distinct identities while keeping the connectivity-of-touch precision
	// problem (you can't reliably drag exactly N px) bounded by the threshold
	// hysteresis — being off by 20 px doesn't change which bucket you land in.
	MaxStock          = 6   // total energy units the buffer can hold (single drag still spends at most 3)
	UnitRecoverFrames = 60  // frames to regenerate one unit (1s @ 60fps); trickles continuously
	SlashMinLength    = 12  // px; below this the drag is treated as noise (no fire, no cost)
	ShortDragMax      = 100 // px drag; <= this lands in the 1-unit bucket
	MidDragMax        = 200 // px drag; <= this lands in the 2-unit bucket (otherwise 3 units)
	// The beam grows from the drag's *start* point in the drag direction
	// (not from the midpoint). We size each bucket so the beam length is
	// noticeably greater than the bucket's max drag distance, which keeps
	// the beam tip out from under the finger during normal play — the only
	// way to land the tip under the finger is to drag farther than 360 px,
	// which is well past any natural swipe.
	ShortLength = 130 // px beam length for a 1-unit slash (> ShortDragMax)
	MidLength   = 240 // px beam length for a 2-unit slash (> MidDragMax)
	LongLength  = 360 // px beam length for a 3-unit slash (tip stays visible up to ~360px drags)
	SlashRevealFrames = 1   // frames for the beam to extend end-to-end (1 = instant snap)
	SlashGlowFrames   = 14  // afterglow frames before the slash effect is removed
	BladeRadius       = 0   // cells (half-thickness of the slash beam; 0 = single-cell hairline. burnSegment patches the 4-connectivity hole that single-cell diagonals would otherwise leave.)
	SlashHitRadius    = 2   // cells (anchored-enemy hit half-width; kept thicker than BladeRadius so a thin beam still feels generous to land)

	// Energy-shortfall feedback. UnfiredTailFrames is how long the red
	// "denied length" tail lingers past the actual beam (post-fire, so
	// the eye is already at the release point). PipFlashFrames is the
	// matching pulse on the consumed HUD pips. Both decay to nothing
	// well inside SlashGlowFrames so successive fast strikes don't
	// stack into one solid blob.
	UnfiredTailFrames = 8
	PipFlashFrames    = 16

	// FeedingSpeedFactor scales an enemy's max movement speed while it
	// is actively eroding lit cells. 0.5 = half speed; turns "drop a
	// line as bait" into a real tactic without removing the long-term
	// erosion threat (rate is unchanged).
	FeedingSpeedFactor = 0.5

	LightThresholdCount = 0.35 // cells with light above this counted as bright
	WallLightThreshold  = 0.5  // cells brighter than this block flood fill

	EnemyErodeRadius = 18.0  // px, footprint of darkening
	EnemyErodeRate   = 0.020 // per frame, interior cells
	EnemyEdgeBoost   = 3.5   // multiplier for cells on the light boundary
	EnemyHomingPx    = 260.0 // search radius for nearest light edge
	EnemyRetargetSec = 0.75  // re-pick a target this often

	// Bind: enemies form a dark enclosure as the mirror image of the player's
	// claim. The phase loops Roaming -> Warning -> Holding; on Holding's last
	// frame, completed edges + existing dark cells are flood-filled to seal
	// any small lit pockets to black.
	BindRoamFrames     = 360 // patrol length before all enemies anchor (6s)
	BindWarnFrames     = 60  // pulse-only warning before the line starts forming
	BindHoldFrames     = 120 // line growth phase; seal happens on its last frame
	BindRangeCells     = 60  // skip pairs farther apart than this (cells)
	BindEdgeWidthCells = 1   // half-thickness of the dark line when rasterized

	// Magic circle: a centered ring drawn as background flavor. The lore is
	// "the outer perimeter seal that holds the dark in, still maintained by
	// previous wards." Mechanically it's a soft leash: enemies prefer light
	// edges inside it and are nudged back if they drift out. The player
	// is unconstrained — slashes can originate and land anywhere on screen.
	// This solves the corner-hugging UX trap (slashes must originate inside
	// the playfield, so wrapping a corner-pinned foe in 3 slashes leaves
	// almost no maneuver room).
	//
	// Radius is sized so the interior is ~32% of the playfield, sitting just
	// below MaxClaimRatio (when that lands) so a perfectly-claimed full
	// interior won't trip the cap.
	MagicCircleCX           = ScreenWidth / 2   // px center X
	MagicCircleCY           = ScreenHeight / 2  // px center Y
	MagicCircleRadiusPx     = 22 * CellSize     // px (176)
	MagicCircleSpawnInsetPx = 4 * CellSize      // px; enemies spawn this far inside the rim
	MagicCircleEdgePushBack = 0.6               // velocity nudge per frame applied when an enemy is at/outside the rim

	// Stage-clear post-process: when a stage clears, the grid is rewritten
	// as the dihedral-4 group's 8 symmetric copies of itself around the
	// magic-circle center (4 rotations + 4 mirror reflections). All 8
	// transforms are integer cell-coordinate operations that align exactly
	// with the 8px grid, so the final mandala has *exact* 8-fold symmetry
	// within the inscribed rectangle — no quantization slop that would
	// otherwise appear if we used 45° rotations on a Cartesian grid (the
	// 45° rotated source cell positions never land on cell centers, so
	// nearest-neighbor sampling makes alternating wedges slightly off-axis
	// and the mandala reads as 4-fold-with-doubling rather than even
	// 8-fold). The 7 non-identity transforms are stamped one per
	// KaleidoFoldFrames so the mandala assembles itself in front of the
	// player; ClearCooldownFrames adds linger time after the animation
	// completes before the player's click is accepted for the next stage.
	KaleidoscopeFolds   = 8  // total dihedral transforms (identity + 7); animation applies 7
	KaleidoFoldFrames   = 8  // frames between transform steps (60 fps; 7×8 = 56 ≈ ~0.9s)
	ClearCooldownFrames = 90 // postClearCooldown for StateCleared (anim + linger)

	// Palette softness: amount of white mixed into the saturated hue
	// returned by hueToRGB. 0 = vivid HSV chroma (the old behavior,
	// reads as harsh primaries on dark background); 1 = pure white. The
	// non-zero floor pushes the palette toward pastel/stained-glass
	// tints, which match the "softly glowing seal" aesthetic better
	// than the raw saturated primaries.
	PaletteSoftness = 0.35
)

// Stage describes one playable level. The 10-stage progression in the GDD
// builds enemy count, speed, and the light-percentage target side by side.
// Stage 10 (the giant boss) will plug into this same shape later.
type Stage struct {
	Enemies    int
	EnemySpeed float64
	TimeLimit  float64
	Boss       bool // spawn one giant; same clear-on-empty rule applies
	EnableBind bool // enemies periodically anchor and weave a dark enclosure

	// HarmlessEnemy makes spawned enemies passive: they drift but their
	// erosion footprint is zero. Used for the stage-1 tutorial so the
	// player can practice the clear loop (slash -> claim) without pressure.
	HarmlessEnemy bool
}

var stages = []Stage{
	{Enemies: 1, EnemySpeed: 0, TimeLimit: 30, HarmlessEnemy: true},      // 1 tutorial: one stationary harmless target parked dead center (see loadStage)
	{Enemies: 2, EnemySpeed: 0, TimeLimit: 45},                           // 2 two stationary foes that DO erode — first taste of time pressure
	{Enemies: 2, EnemySpeed: 0.40, TimeLimit: 45},                        // 3 enemies start moving
	{Enemies: 2, EnemySpeed: 0.55, TimeLimit: 55, EnableBind: true},      // 4 bind intro
	{Enemies: 3, EnemySpeed: 0.55, TimeLimit: 55, EnableBind: true},      // 5
	{Enemies: 3, EnemySpeed: 0.70, TimeLimit: 60, EnableBind: true},      // 6
	{Enemies: 4, EnemySpeed: 0.70, TimeLimit: 60, EnableBind: true},      // 7
	{Enemies: 3, EnemySpeed: 0.90, TimeLimit: 65, EnableBind: true},      // 8
	{Enemies: 4, EnemySpeed: 0.90, TimeLimit: 65, EnableBind: true},      // 9
	{Enemies: 0, EnemySpeed: 0.35, TimeLimit: 90, Boss: true},            // 10 boss
}

// DebugMode is initialized in debug_mode_default.go / debug_mode_wasm.go.
var DebugMode bool

type Cell struct {
	Light   float32
	R, G, B float32
	// Claimed marks cells that were filled by claimEnclosure. The flag is the
	// anchor reviewClaims() uses to decide whether a previously sealed pocket
	// has been chewed open and should revert to darkness — the Light value
	// alone isn't enough because a freshly burned slash cell looks identical
	// to a claim cell while both are at 1.0.
	Claimed bool
}

// Slash is one in-flight strike. It owns its own hue (captured at fire time so
// reload-induced hue changes don't recolor mid-extension), advances its tip by
// one Reveal slice per frame, runs claimEnclosure once on full extension, then
// lingers for SlashGlowFrames as visual residue.
//
// HasUnfired / Unfired{From,To} encode "what would have fired if you had
// enough energy" — set when fireSlash quantizes the drag into a bigger
// bucket than the player can afford. The tail is rendered as red dashes for
// a few frames so the player sees the part of the beam the energy budget
// denied them, instead of silently getting a shorter line.
type Slash struct {
	X0, Y0       float64
	X1, Y1       float64
	Hue          float64
	Frame        int
	claimed      bool
	HasUnfired   bool
	UnfiredFromX float64
	UnfiredFromY float64
	UnfiredToX   float64
	UnfiredToY   float64
}

type Enemy struct {
	X, Y         float64
	VX, VY       float64
	Speed        float64
	Radius       float64 // visual radius in px
	EffectRadius float64 // erosion footprint in px
	IsBoss       bool
	HasTarget    bool
	TargetX      int // grid cell
	TargetY      int
	TargetAge    int // frames since last retarget

	// ID is a stable identifier for severed-pair tracking; assigned in
	// loadStage. Zero means "not assigned" (boss/tutorial enemies).
	ID int

	// EdgeGlow tracks how recently this enemy pushed against the magic
	// circle rim. Ramps to 1 on contact, decays per frame; Draw uses it
	// to flash the seal where the dark touched it.
	EdgeGlow float64

	// Feeding is set whenever the last erodeAround actually darkened a
	// lit cell. While true, steerEnemy halves the enemy's max speed —
	// so a slash drawn as bait stalls a foe that drifts onto it, opening
	// a window to encircle them with the next strikes.
	Feeding bool
}

type GameState int

const (
	StateTitle GameState = iota
	StatePlaying
	StateCleared
	StateGameOver
	StateAllCleared
)

type Game struct {
	state GameState

	grid    [GridWidth][GridHeight]Cell
	gridImg *ebiten.Image
	pixels  []byte

	enemies []*Enemy
	slashes []*Slash

	currentStage       int
	dragging           bool
	slashStartX        int
	slashStartY        int
	pointerX, pointerY int // tracked every frame for slash preview drawing
	stock              int
	reloadProgress     float64 // 0..1 toward the next stock refill
	hue                float64
	stageTime          float64
	lightPercent       float64
	rng                *rand.Rand
	postClearCooldown  int

	// Bind phase state. All enemies on a bind-enabled stage share one timer:
	// 0 (Roaming) -> 1 (Warning) -> 2 (Holding) -> seal -> 0.
	bindPhase       int
	bindPhaseFrames int
	severedPairs    map[[2]int]bool

	// Onboarding state. tutorialStep is gated on game events, not stroke
	// count, because three slashes don't always form a sealable enclosure
	// (parallel lines, lines that only touch the border, etc.). Step 0:
	// before any slash. Step 1: at least one slash fired, waiting on a
	// successful claim. Step 2 onward: hint suppressed.
	// sealedFlashFrames counts down a brief SEALED! center flash whenever
	// claimEnclosure removes one or more enemies, so the player feels the
	// causal link between the closing slash and the kill.
	tutorialStep      int
	sealedFlashFrames int

	// Stage-clear kaleidoscope animation state. On StateCleared entry the
	// current grid is snapshotted into kaleidoSnapshot and the
	// KaleidoscopeFolds-1 rotated copies are stamped onto the live grid
	// one fold every KaleidoFoldFrames frames, so the mandala assembles
	// itself in front of the player instead of popping in. kaleidoNextFold
	// is the next rotation index (1..KaleidoscopeFolds-1) waiting to be
	// applied; 0 means idle, >= KaleidoscopeFolds means done.
	kaleidoSnapshot  [GridWidth][GridHeight]Cell
	kaleidoNextFold  int
	kaleidoFoldTimer int

	// Pip flash: brief red overlay on the HUD pips that were just spent
	// by a slash. Fast-drag players don't track the ghost preview during
	// the drag, so the energy-cost feedback has to land at release time
	// where the eye is already looking. pipFlashFrames decays per frame;
	// pipFlashFromIdx + pipFlashCount identify the pip slots to overlay.
	pipFlashFrames  int
	pipFlashFromIdx int
	pipFlashCount   int

	// Fonts are shared by every drawing path. faceLarge is reserved for
	// hero text (title, big mid-screen flashes); faceMid for HUD primary
	// numbers and tutorial hints; faceSmall for footnotes and reload-state
	// callouts.
	faceLarge *text.GoTextFace
	faceMid   *text.GoTextFace
	faceSmall *text.GoTextFace
}

func newGame() *Game {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(fonts.MPlus1pRegular_ttf))
	if err != nil {
		log.Fatal(err)
	}
	g := &Game{
		state:     StateTitle,
		stock:     MaxStock,
		rng:       rand.New(rand.NewSource(20260622)),
		pixels:    make([]byte, GridWidth*GridHeight*4),
		faceLarge: &text.GoTextFace{Source: src, Size: 32},
		faceMid:   &text.GoTextFace{Source: src, Size: 16},
		faceSmall: &text.GoTextFace{Source: src, Size: 12},
	}
	g.loadStage(0)
	return g
}

// loadStage clears the field and seeds enemies according to the given index.
// idx is clamped to the stages table; out-of-range jumps to all-cleared.
func (g *Game) loadStage(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(stages) {
		g.state = StateAllCleared
		g.postClearCooldown = 60
		return
	}
	g.currentStage = idx
	s := stages[idx]

	for x := 0; x < GridWidth; x++ {
		for y := 0; y < GridHeight; y++ {
			g.grid[x][y] = Cell{}
		}
	}
	g.stock = MaxStock
	g.reloadProgress = 0
	g.stageTime = s.TimeLimit
	g.dragging = false
	g.lightPercent = 0
	g.hue = g.rng.Float64() * 360

	g.enemies = g.enemies[:0]
	g.slashes = g.slashes[:0]
	g.bindPhase = 0
	g.bindPhaseFrames = 0
	g.severedPairs = map[[2]int]bool{}
	g.tutorialStep = 0
	g.sealedFlashFrames = 0
	g.pipFlashFrames = 0
	g.pipFlashFromIdx = 0
	g.pipFlashCount = 0
	if s.Boss {
		g.enemies = append(g.enemies, &Enemy{
			X:            ScreenWidth / 2,
			Y:            ScreenHeight / 2,
			VX:           s.EnemySpeed,
			VY:           s.EnemySpeed * 0.6,
			Speed:        s.EnemySpeed,
			Radius:       70,
			EffectRadius: 56,
			IsBoss:       true,
		})
		return
	}
	effectR := float64(EnemyErodeRadius)
	if s.HarmlessEnemy {
		effectR = 0
	}
	spawnR := float64(MagicCircleRadiusPx - MagicCircleSpawnInsetPx)
	for i := 0; i < s.Enemies; i++ {
		// Stage 1 (tutorial) parks its lone foe dead center so the
		// "ENCLOSE" target is unambiguous and stationary. EnemySpeed
		// is 0 for that stage, so the random initial velocity below
		// resolves to zero and the steerer can't push it either.
		var ex, ey float64
		if idx == 0 {
			ex, ey = ScreenWidth/2, ScreenHeight/2
		} else {
			// Uniform-area sample inside the magic circle interior
			// (sqrt on the radial draw avoids center clustering).
			// This keeps foes off the screen edges, where wrapping
			// them in a polygon would leave no maneuver room.
			angle := g.rng.Float64() * 2 * math.Pi
			r := math.Sqrt(g.rng.Float64()) * spawnR
			ex = float64(MagicCircleCX) + r*math.Cos(angle)
			ey = float64(MagicCircleCY) + r*math.Sin(angle)
		}
		g.enemies = append(g.enemies, &Enemy{
			ID:           i + 1,
			X:            ex,
			Y:            ey,
			VX:           (g.rng.Float64()*2 - 1) * s.EnemySpeed,
			VY:           (g.rng.Float64()*2 - 1) * s.EnemySpeed,
			Speed:        s.EnemySpeed,
			Radius:       12,
			EffectRadius: effectR,
		})
	}
}

// pointerPos returns the current pointer position and whether it's pressed.
// Touch wins over mouse so taps/swipes on mobile drive the blade. The first
// active touch ID is used; multi-touch is ignored.
func pointerPos() (x, y int, pressed bool) {
	if ids := ebiten.AppendTouchIDs(nil); len(ids) > 0 {
		tx, ty := ebiten.TouchPosition(ids[0])
		return tx, ty, true
	}
	mx, my := ebiten.CursorPosition()
	return mx, my, ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
}

// pointerJustPressed is true on the first frame of a mouse click or touch.
func pointerJustPressed() bool {
	if len(inpututil.AppendJustPressedTouchIDs(nil)) > 0 {
		return true
	}
	return inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
}

// pointerJustReleased is true on the frame the mouse button or touch ends.
// Menu/state transitions key off release rather than press so the click that
// dismisses a screen can't double as the start of a slash drag in the next
// state — the player visibly finishes the gesture before the world changes.
func pointerJustReleased() bool {
	if len(inpututil.AppendJustReleasedTouchIDs(nil)) > 0 {
		return true
	}
	return inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft)
}

func (g *Game) Update() error {
	switch g.state {
	case StateTitle:
		if pointerJustReleased() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.loadStage(0)
			g.state = StatePlaying
			playSFX(sfxConfirm)
			playBGM()
		}
	case StatePlaying:
		g.updatePlaying()
	case StateCleared:
		if g.postClearCooldown > 0 {
			g.postClearCooldown--
			// Drive the mandala animation alongside the cooldown so the
			// 7 rotated copies stamp in one-per-KaleidoFoldFrames over
			// roughly the first second of the clear screen. The cooldown
			// also acts as an input-grace window so the same click that
			// finished off the last enemy can't immediately skip past
			// the seal animation.
			g.tickKaleidoscope()
			break
		}
		// Mandala is complete; wait for the player to acknowledge before
		// advancing. Mirrors StateGameOver / StateAllCleared so the seal
		// gets a held beat rather than auto-scrolling out from under the
		// player.
		if pointerJustReleased() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.loadStage(g.currentStage + 1)
			if g.state != StateAllCleared {
				g.state = StatePlaying
				playSFX(sfxConfirm)
				playBGM()
			} else {
				playSFX(sfxConfirm)
			}
		}
	case StateGameOver:
		if g.postClearCooldown > 0 {
			g.postClearCooldown--
			break
		}
		if pointerJustReleased() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.loadStage(g.currentStage)
			g.state = StatePlaying
			playSFX(sfxConfirm)
			playBGM()
		}
	case StateAllCleared:
		if g.postClearCooldown > 0 {
			g.postClearCooldown--
			break
		}
		if pointerJustReleased() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.state = StateTitle
			playSFX(sfxConfirm)
		}
	}
	return nil
}

func (g *Game) updatePlaying() {
	mx, my, pressed := pointerPos()

	// Only sample the pointer while it's actually pressed. On touch, the
	// release frame returns (0,0) because the touch ID is already gone — if
	// we used that as the slash endpoint, every strike would shoot toward
	// the top-left of the screen. Holding the last pressed-frame value
	// instead gives us a valid release position for fireSlash.
	if pressed {
		g.pointerX, g.pointerY = mx, my
		if !g.dragging && g.stock >= 1 {
			g.dragging = true
			g.slashStartX, g.slashStartY = mx, my
			g.hue += 47 + g.rng.Float64()*30
		}
	} else if g.dragging {
		g.fireSlash(g.slashStartX, g.slashStartY, g.pointerX, g.pointerY)
		g.dragging = false
	}

	g.updateSlashes()

	// Energy regenerates one unit at a time, continuously. Trickling like
	// this is what makes "spend a quick 1-unit dab, then keep firing as
	// units re-arrive" feel different from "spend a 3-unit haymaker and
	// wait." reloadProgress is the 0..1 progress toward the *next* unit
	// (not toward a full magazine), so the HUD shows a steadily-rising bar
	// rather than a long all-or-nothing wait.
	if g.stock < MaxStock {
		g.reloadProgress += 1.0 / float64(UnitRecoverFrames)
		if g.reloadProgress >= 1.0 {
			g.stock++
			g.reloadProgress = 0
		}
	} else {
		g.reloadProgress = 0
	}

	g.advanceBindPhase()
	anchored := g.isAnchoring()

	for _, e := range g.enemies {
		if anchored && !e.IsBoss {
			// Vertices freeze while the dark line forms — no steering,
			// no movement, no erosion. The boss never anchors.
			continue
		}
		g.steerEnemy(e)
		e.X += e.VX
		e.Y += e.VY

		// Edge glow decays smoothly so the rim flash reads as a soft
		// pulse rather than a hard flicker.
		e.EdgeGlow *= 0.9

		if e.IsBoss {
			// The boss isn't leashed to the magic circle — it owns the
			// whole stage. Keep the rectangular bounce so it stays on
			// screen with bad initial velocity.
			margin := e.Radius + 8
			if e.X < margin {
				e.X = margin
				e.VX = -e.VX
			}
			if e.X > ScreenWidth-margin {
				e.X = ScreenWidth - margin
				e.VX = -e.VX
			}
			if e.Y < margin {
				e.Y = margin
				e.VY = -e.VY
			}
			if e.Y > ScreenHeight-margin {
				e.Y = ScreenHeight - margin
				e.VY = -e.VY
			}
		} else {
			// Magic-circle leash: small foes push back into the seal
			// when they drift past the rim. Combined with the AI
			// preferring inside-rim targets (findNearestLightEdge),
			// they live entirely inside the circle without anyone
			// noticing a "wall."
			dxc := e.X - float64(MagicCircleCX)
			dyc := e.Y - float64(MagicCircleCY)
			dc := math.Hypot(dxc, dyc)
			maxR := float64(MagicCircleRadiusPx) - e.Radius
			if dc > maxR && dc > 0.5 {
				e.VX -= (dxc / dc) * MagicCircleEdgePushBack
				e.VY -= (dyc / dc) * MagicCircleEdgePushBack
				// Hard clamp so foes never bleed deep into the
				// dead zone even with momentum spikes.
				e.X = float64(MagicCircleCX) + dxc*maxR/dc
				e.Y = float64(MagicCircleCY) + dyc*maxR/dc
				e.EdgeGlow = 1
			}
		}
		// Rising-edge SE: only on the frame Feeding flips false→true so
		// the bite is a discrete "chomp", not a continuous drone. The
		// enemy keeps eating while Feeding stays true; the audio cue
		// just marks the start of each engagement.
		wasFeeding := e.Feeding
		e.Feeding = g.erodeAround(e.X, e.Y, e.EffectRadius)
		if !wasFeeding && e.Feeding {
			playSFX(sfxErode)
		}
	}

	// Claims are not permanent — once the perimeter is chewed through, the
	// pocket reverts. Runs after erosion so the same frame's damage is
	// reflected immediately.
	g.reviewClaims()

	if g.sealedFlashFrames > 0 {
		g.sealedFlashFrames--
	}
	if g.pipFlashFrames > 0 {
		g.pipFlashFrames--
	}

	g.stageTime -= 1.0 / 60.0
	if g.stageTime <= 0 {
		g.state = StateGameOver
		g.postClearCooldown = 45
		stopBGM()
		playSFX(sfxGameOver)
		return
	}

	count := 0
	total := GridWidth * GridHeight
	for x := 0; x < GridWidth; x++ {
		for y := 0; y < GridHeight; y++ {
			if g.grid[x][y].Light > LightThresholdCount {
				count++
			}
		}
	}
	g.lightPercent = float64(count) / float64(total)

	// Unified clear rule: every stage (including the boss) is cleared the
	// frame no enemies remain. Light percent is now a feedback HUD only.
	if len(g.enemies) == 0 {
		g.state = StateCleared
		g.postClearCooldown = ClearCooldownFrames
		// Arm the mandala animation. tickKaleidoscope runs every frame
		// during the cooldown and stamps one rotated copy at a time, so
		// the player watches the sealed pattern assemble itself instead
		// of seeing it pop in.
		g.beginKaleidoscope()
		stopBGM()
		playSFX(sfxClear)
	}
}

// slashSpec maps (drag distance, current stock) to the slash that would fire.
// Quantizing into three buckets keeps "I meant to dab" from being misread as
// "I meant to swing" — being off by 20 px doesn't change the result. When the
// player asks for more units than they have, the request is downgraded to
// whatever they can afford (so a big swipe with 1 unit left still fires, just
// shorter). Returns ok=false when the drag is below SlashMinLength or there's
// no energy at all to spend.
func slashSpec(dragDist float64, stock int) (units int, length float64, ok bool) {
	if dragDist < SlashMinLength || stock <= 0 {
		return 0, 0, false
	}
	switch {
	case dragDist < ShortDragMax:
		units = 1
	case dragDist < MidDragMax:
		units = 2
	default:
		units = 3
	}
	if units > stock {
		units = stock
	}
	return units, lengthForUnits(units), true
}

func lengthForUnits(u int) float64 {
	switch u {
	case 1:
		return ShortLength
	case 2:
		return MidLength
	case 3:
		return LongLength
	}
	return 0
}

// bucketedUnitsForDrag is slashSpec's bucket choice with the stock cap
// removed. fireSlash compares this against the actually-fired unit count
// to know whether the player was energy-downgraded — i.e. they swung big
// but only got a 1-unit beam because they were out of stock — and if so,
// it stashes the missing tail on the Slash so Draw can flash it red.
func bucketedUnitsForDrag(dragDist float64) int {
	switch {
	case dragDist < SlashMinLength:
		return 0
	case dragDist < ShortDragMax:
		return 1
	case dragDist < MidDragMax:
		return 2
	default:
		return 3
	}
}

// fireSlash spawns a slash that starts at the drag's start point and extends
// in the drag direction for the bucketed length from slashSpec. This means
// the place the player first touched is the strike origin and the beam fans
// out forward, which (a) reads as "swing from here" rather than "explode
// around here", and (b) keeps the beam tip out from under the finger as long
// as the bucket's max drag is shorter than its beam length.
func (g *Game) fireSlash(x0, y0, x1, y1 int) {
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	dist := math.Hypot(dx, dy)
	units, length, ok := slashSpec(dist, g.stock)
	if !ok {
		return
	}
	nx := dx / dist
	ny := dy / dist
	sx0 := float64(x0)
	sy0 := float64(y0)
	sx1 := sx0 + nx*length
	sy1 := sy0 + ny*length

	// Energy-shortfall tail: if the drag would have bought a bigger beam
	// but the stock cap downgraded it, capture the missing tail in
	// pre-clip coords so Draw can render the denied length as red dashes.
	// Computed BEFORE the clip mutates sx0..sy1 so the tail's "from" is
	// the unclipped actual tip, then clipped to the circle so it can't
	// bleed outside the seal.
	bucketed := bucketedUnitsForDrag(dist)
	hasUnfired := false
	var ufx0, ufy0, ufx1, ufy1 float64
	if units < bucketed {
		reqLen := lengthForUnits(bucketed)
		rx := sx0 + nx*reqLen
		ry := sy0 + ny*reqLen
		a0, b0, a1, b1, tailInside := clipSegmentToCircle(sx1, sy1, rx, ry)
		if tailInside {
			hasUnfired = true
			ufx0, ufy0, ufx1, ufy1 = a0, b0, a1, b1
		}
	}

	// Clip the beam to the magic circle. If the entire stroke would land
	// outside the seal, refund the energy and abort silently — the ghost
	// preview hid itself for the same reason, so the player has already
	// been told "this isn't going to fire."
	cx0, cy0, cx1, cy1, inside := clipSegmentToCircle(sx0, sy0, sx1, sy1)
	if !inside {
		return
	}
	sx0, sy0, sx1, sy1 = cx0, cy0, cx1, cy1
	// Mark the pips that are about to drain BEFORE decrementing stock so
	// pipFlashFromIdx points at the first consumed slot.
	g.pipFlashFromIdx = g.stock - units
	g.pipFlashCount = units
	g.pipFlashFrames = PipFlashFrames
	g.stock -= units
	if g.currentStage == 0 && g.tutorialStep == 0 {
		// First slash fired: advance to "ENCLOSE" prompt. Further advances
		// happen when claimEnclosure actually seals a pocket, not on stroke
		// count — three strokes don't guarantee a sealable shape.
		g.tutorialStep = 1
		// Re-park the tutorial foe at the candidate cell that sits farthest
		// from this beam, so the player's first stroke never crosses where
		// the enemy ends up. Center is included in the candidate set, so
		// the foe only moves if the beam actually came near it.
		g.repositionTutorialFoeAwayFrom(sx0, sy0, sx1, sy1)
	}
	g.slashes = append(g.slashes, &Slash{
		X0:           sx0,
		Y0:           sy0,
		X1:           sx1,
		Y1:           sy1,
		Hue:          g.hue,
		HasUnfired:   hasUnfired,
		UnfiredFromX: ufx0,
		UnfiredFromY: ufy0,
		UnfiredToX:   ufx1,
		UnfiredToY:   ufy1,
	})

	// Slash SE keys off unit count so 1-unit dabs sound lighter than 2/3-unit
	// haymakers. Reuses the existing quantization rather than introducing a
	// separate audio threshold.
	if units >= 2 {
		playSFX(sfxSlash)
	} else {
		playSFX(sfxSlashShort)
	}

	// Anchored enemies are vulnerable: a slash that grazes them removes the
	// vertex (and any pair that included it stops contributing to the seal).
	// Sever any remaining dark lines that the beam crossed. Both checks are
	// gated on isAnchoring so during normal patrol the slash still only
	// affects light, not enemies.
	if g.isAnchoring() {
		bladePx := float64(SlashHitRadius * CellSize)
		before := len(g.enemies)
		survivors := g.enemies[:0]
		for _, e := range g.enemies {
			if !e.IsBoss && pointSegmentDistance(e.X, e.Y, sx0, sy0, sx1, sy1) <= e.Radius+bladePx {
				continue // sealed/cut: this anchored enemy is removed
			}
			survivors = append(survivors, e)
		}
		g.enemies = survivors
		if len(g.enemies) < before {
			playSFX(sfxEnemyDown)
		}
		for _, pair := range g.bindEdgesNow() {
			if segmentsIntersect(pair[0].X, pair[0].Y, pair[1].X, pair[1].Y, sx0, sy0, sx1, sy1) {
				g.severedPairs[pairKey(pair[0].ID, pair[1].ID)] = true
			}
		}
	}
}

// updateSlashes advances every active slash by one frame. While the tip is
// still extending it burns the newly-covered segment into the grid; the
// frame the tip reaches the end it runs claimEnclosure once; afterwards it
// just decays for SlashGlowFrames before being removed. Stock is unrelated:
// it ticks down at fire time and refills on its own schedule.
func (g *Game) updateSlashes() {
	if len(g.slashes) == 0 {
		return
	}
	keep := g.slashes[:0]
	for _, s := range g.slashes {
		prev := s.Frame
		s.Frame++
		if prev < SlashRevealFrames {
			t0 := float64(prev) / float64(SlashRevealFrames)
			t1 := float64(s.Frame) / float64(SlashRevealFrames)
			if t1 > 1 {
				t1 = 1
			}
			g.burnSegment(s, t0, t1)
			if !s.claimed && t1 >= 1 {
				saved := g.hue
				g.hue = s.Hue
				g.claimEnclosure()
				g.hue = saved
				s.claimed = true
			}
		}
		if s.Frame <= SlashRevealFrames+SlashGlowFrames {
			keep = append(keep, s)
		}
	}
	g.slashes = keep
}

// burnSegment illuminates the portion of slash s between parametric points
// t0 and t1 (0=anchor, 1=tip). Temporarily borrows the global hue so the
// existing illuminate() routine paints in this slash's color.
//
// With BladeRadius=0 the painted footprint is a single cell, so a 45° slash
// can step from cell (a,a) to (a+1,a+1) in one ~1px sample — that diagonal
// jump leaves the line 8-connected but not 4-connected, and claimEnclosure's
// 4-connected flood would leak through the gap. We track the previous cell
// and, whenever both coordinates change in one step, paint BOTH orthogonal
// intermediates. One alone (L-shape) keeps the flood safe but looks like a
// zig-zag staircase to the eye — readers report it as "the line is broken."
// Painting both gives every diagonal step a 2x2 block, which reads as a
// continuous thick line without changing the claim geometry meaningfully.
func (g *Game) burnSegment(s *Slash, t0, t1 float64) {
	if t1 <= t0 {
		return
	}
	dx := s.X1 - s.X0
	dy := s.Y1 - s.Y0
	segLen := math.Hypot(dx, dy) * (t1 - t0)
	steps := int(segLen) + 1
	saved := g.hue
	g.hue = s.Hue
	var prevCX, prevCY int
	havePrev := false
	for i := 0; i <= steps; i++ {
		u := t0 + (t1-t0)*float64(i)/float64(steps)
		px := int(s.X0 + dx*u)
		py := int(s.Y0 + dy*u)
		cx := px / CellSize
		cy := py / CellSize
		if havePrev && cx != prevCX && cy != prevCY {
			// Diagonal jump — fill both orthogonal neighbours so the
			// step closes into a 2x2 block instead of an L kink.
			g.illuminate(prevCX*CellSize+CellSize/2, cy*CellSize+CellSize/2)
			g.illuminate(cx*CellSize+CellSize/2, prevCY*CellSize+CellSize/2)
		}
		g.illuminate(px, py)
		prevCX, prevCY = cx, cy
		havePrev = true
	}
	g.hue = saved
}

func (g *Game) illuminate(px, py int) {
	r, gC, b := hueToRGB(g.hue)
	cx := px / CellSize
	cy := py / CellSize
	for dx := -BladeRadius; dx <= BladeRadius; dx++ {
		for dy := -BladeRadius; dy <= BladeRadius; dy++ {
			x := cx + dx
			y := cy + dy
			if x < 0 || x >= GridWidth || y < 0 || y >= GridHeight {
				continue
			}
			d2 := dx*dx + dy*dy
			if d2 > BladeRadius*BladeRadius {
				continue
			}
			falloff := 1.0 - float32(math.Sqrt(float64(d2)))/float32(BladeRadius+1)
			c := &g.grid[x][y]
			newLight := c.Light + falloff*0.6
			if newLight > 1 {
				newLight = 1
			}
			c.Light = newLight
			// Blend toward the current hue. Already-colored cells get nudged.
			c.R = c.R*0.55 + r*0.45
			c.G = c.G*0.55 + gC*0.45
			c.B = c.B*0.55 + b*0.45
		}
	}
}

// steerEnemy nudges the enemy toward the nearest light edge. It re-targets
// periodically (or when the target has gone dark). Without a target it drifts
// on its existing velocity.
func (g *Game) steerEnemy(e *Enemy) {
	e.TargetAge++
	retargetFrames := int(EnemyRetargetSec * 60)
	if e.HasTarget {
		t := &g.grid[e.TargetX][e.TargetY]
		if t.Light < WallLightThreshold {
			e.HasTarget = false
		}
	}
	if !e.HasTarget || e.TargetAge >= retargetFrames {
		if tx, ty, ok := g.findNearestLightEdge(e.X, e.Y, EnemyHomingPx); ok {
			e.HasTarget = true
			e.TargetX, e.TargetY = tx, ty
			e.TargetAge = 0
		}
	}
	if !e.HasTarget {
		return
	}

	tx := float64(e.TargetX*CellSize + CellSize/2)
	ty := float64(e.TargetY*CellSize + CellSize/2)
	dx := tx - e.X
	dy := ty - e.Y
	d := math.Hypot(dx, dy)
	if d < 1 {
		return
	}
	const steer = 0.25
	// A foe that is actively eroding lit cells is "feeding" — halve its
	// max speed so a bait line really stalls it. The feeding flag is set
	// by last frame's erodeAround, so the slowdown lags movement by one
	// frame; imperceptible at 60fps and avoids re-ordering the loop.
	maxSpeed := e.Speed
	if e.Feeding {
		maxSpeed *= FeedingSpeedFactor
	}
	e.VX = e.VX*(1-steer) + (dx/d)*maxSpeed*steer
	e.VY = e.VY*(1-steer) + (dy/d)*maxSpeed*steer
	if sp := math.Hypot(e.VX, e.VY); sp > maxSpeed {
		e.VX *= maxSpeed / sp
		e.VY *= maxSpeed / sp
	}
}

// findNearestLightEdge returns the closest lit cell that has a dark neighbour
// (the boundary of a lit region). Restricted to a px-radius window for cost.
func (g *Game) findNearestLightEdge(ex, ey, maxPx float64) (int, int, bool) {
	cx0 := int(ex) / CellSize
	cy0 := int(ey) / CellSize
	R := int(maxPx/CellSize) + 1
	bestD2 := -1
	var bx, by int
	for dy := -R; dy <= R; dy++ {
		for dx := -R; dx <= R; dx++ {
			x := cx0 + dx
			y := cy0 + dy
			if x < 0 || x >= GridWidth || y < 0 || y >= GridHeight {
				continue
			}
			if g.grid[x][y].Light < WallLightThreshold {
				continue
			}
			// Skip targets outside the magic circle so foes don't
			// chase light into corners where they can't comfortably
			// reach. Paired with the push-back in updatePlaying this
			// keeps enemies "living inside the seal."
			ccx := float64(x*CellSize+CellSize/2) - float64(MagicCircleCX)
			ccy := float64(y*CellSize+CellSize/2) - float64(MagicCircleCY)
			if ccx*ccx+ccy*ccy > float64(MagicCircleRadiusPx)*float64(MagicCircleRadiusPx) {
				continue
			}
			edge := false
			for _, n := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+n[0], y+n[1]
				if nx < 0 || nx >= GridWidth || ny < 0 || ny >= GridHeight {
					continue
				}
				if g.grid[nx][ny].Light < WallLightThreshold {
					edge = true
					break
				}
			}
			if !edge {
				continue
			}
			d2 := dx*dx + dy*dy
			if bestD2 < 0 || d2 < bestD2 {
				bestD2 = d2
				bx, by = x, y
			}
		}
	}
	if bestD2 < 0 {
		return 0, 0, false
	}
	return bx, by, true
}

// erodeAround dims lit cells around the point. Cells on the light boundary
// (adjacent to a dark cell) erode faster, giving the visible "chewing the
// edge" effect. Returns true if any lit cell was actually darkened — used
// upstream to flag the enemy as "feeding" for the bait-line slowdown.
func (g *Game) erodeAround(x, y, radius float64) bool {
	if radius <= 0 {
		return false
	}
	cx := int(x) / CellSize
	cy := int(y) / CellSize
	rcell := int(radius/CellSize) + 1
	eroded := false
	for dx := -rcell; dx <= rcell; dx++ {
		for dy := -rcell; dy <= rcell; dy++ {
			px := cx + dx
			py := cy + dy
			if px < 0 || px >= GridWidth || py < 0 || py >= GridHeight {
				continue
			}
			d := math.Sqrt(float64(dx*dx + dy*dy))
			if d > float64(rcell) {
				continue
			}
			c := &g.grid[px][py]
			if c.Light <= 0 {
				continue
			}
			edge := false
			for _, n := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := px+n[0], py+n[1]
				if nx < 0 || nx >= GridWidth || ny < 0 || ny >= GridHeight {
					continue
				}
				if g.grid[nx][ny].Light < WallLightThreshold {
					edge = true
					break
				}
			}
			falloff := float32(1.0 - d/float64(rcell+1))
			rate := float32(EnemyErodeRate)
			if edge {
				rate *= EnemyEdgeBoost
			}
			c.Light -= falloff * rate
			if c.Light < 0 {
				c.Light = 0
			}
			eroded = true
		}
	}
	return eroded
}

// claimEnclosure splits the playfield (everything inside the always-wall
// 1-cell border ring) into 4-connected dark components and claims every
// component that does NOT touch the playfield border. Components touching
// the border are treated as "outside" and left alone, so two parallel
// slashes that just split the field into bands no longer cheese the level
// — only a fully enclosed pocket (carved entirely by slashes, with no
// border contact) seals. This forces the player to commit to the 3-line
// triangle ritual rather than chopping the screen in half.
//
// Enemies whose centers fall inside a claimed component are sealed
// (removed). Run once per slash, after the beam reaches full length.
func (g *Game) claimEnclosure() {
	const wall = WallLightThreshold

	var compID [GridWidth][GridHeight]int // 0 = wall or border
	var compTouchesBorder []bool          // index = id-1
	nextID := 1
	queue := make([][2]int, 0, 512)

	for sx := 1; sx < GridWidth-1; sx++ {
		for sy := 1; sy < GridHeight-1; sy++ {
			if compID[sx][sy] != 0 || g.grid[sx][sy].Light >= wall {
				continue
			}
			compID[sx][sy] = nextID
			queue = append(queue[:0], [2]int{sx, sy})
			touchesBorder := false
			for len(queue) > 0 {
				p := queue[0]
				queue = queue[1:]
				if p[0] == 1 || p[0] == GridWidth-2 || p[1] == 1 || p[1] == GridHeight-2 {
					touchesBorder = true
				}
				// Cells outside the magic circle act as if they touch
				// the screen border, so claims that wrap around or
				// extend outside the seal can't sweep leashed foes.
				if cellOutsideMagicCircle(p[0], p[1]) {
					touchesBorder = true
				}
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p[0]+d[0], p[1]+d[1]
					if nx < 1 || nx >= GridWidth-1 || ny < 1 || ny >= GridHeight-1 {
						continue
					}
					if compID[nx][ny] != 0 || g.grid[nx][ny].Light >= wall {
						continue
					}
					compID[nx][ny] = nextID
					queue = append(queue, [2]int{nx, ny})
				}
			}
			compTouchesBorder = append(compTouchesBorder, touchesBorder)
			nextID++
		}
	}

	if len(compTouchesBorder) == 0 {
		return
	}

	r, gC, b := hueToRGB(g.hue)
	var inside [GridWidth][GridHeight]bool
	claimed := 0
	for x := 1; x < GridWidth-1; x++ {
		for y := 1; y < GridHeight-1; y++ {
			id := compID[x][y]
			if id == 0 || compTouchesBorder[id-1] {
				continue
			}
			inside[x][y] = true
			claimed++
			c := &g.grid[x][y]
			c.Light = 1
			c.R, c.G, c.B = r, gC, b
			c.Claimed = true
		}
	}
	if claimed == 0 {
		return
	}

	// An enemy whose center sits directly on a wall cell (typically the just-
	// burned slash beam) is neither "inside" nor part of any component, so a
	// strict inside[cx][cy] test lets enemies riding the closing edge slip
	// through. Fall back to 4-neighbor probing in that case: if any adjacent
	// cell belongs to the claimed component, treat the enemy as sealed.
	sealed := func(cx, cy int) bool {
		if cx < 0 || cx >= GridWidth || cy < 0 || cy >= GridHeight {
			return false
		}
		if inside[cx][cy] {
			return true
		}
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := cx+d[0], cy+d[1]
			if nx < 0 || nx >= GridWidth || ny < 0 || ny >= GridHeight {
				continue
			}
			if inside[nx][ny] {
				return true
			}
		}
		return false
	}
	before := len(g.enemies)
	survivors := g.enemies[:0]
	for _, e := range g.enemies {
		cx := int(e.X) / CellSize
		cy := int(e.Y) / CellSize
		if sealed(cx, cy) {
			continue
		}
		survivors = append(survivors, e)
	}
	g.enemies = survivors
	// Seal "thunk" plays on every successful claim, even territory-only
	// grabs that don't trap anyone — it's the "the pocket closed" beat.
	if claimed > 0 {
		playSFX(sfxSeal)
	}
	if len(g.enemies) < before {
		// Frame budget for the SEALED! flash. 30 frames at 60fps is half a
		// second — long enough to register, short enough that the next
		// strike doesn't have to wait for it to clear.
		g.sealedFlashFrames = 30
		// First successful seal also retires the stage-1 ENCLOSE hint —
		// the player has demonstrated the full loop.
		if g.currentStage == 0 && g.tutorialStep < 2 {
			g.tutorialStep = 2
		}
		// Single sfxEnemyDown even when multiple foes are sealed in one
		// shot — stacked players pile amplitude into a wall of noise on
		// lucky multi-claims.
		playSFX(sfxEnemyDown)
	}
}

// reviewClaims re-evaluates every claimed pocket against the current grid
// state and revokes any pocket whose perimeter has been gnawed open. The
// rule is the mirror of claimEnclosure's original judgement: a claim is
// only valid while its cells stay cut off from the playfield border by
// wall cells. The moment an enemy chews enough of the surrounding slash
// beam below WallLightThreshold to reconnect the pocket to the outside
// dark region, the seal is forfeit and the pocket goes back to Light=0.
//
// This is what makes enemies actually dangerous: a finished triangle is
// no longer a permanent score, it's territory that has to be defended.
// Without re-evaluation, erodeAround would slowly carve holes in claim
// borders but the inside cells would keep glowing forever.
func (g *Game) reviewClaims() {
	const wall = WallLightThreshold

	// Pass 1: 4-connected components of dark cells, with a flag for whether
	// each component reaches the playfield border (=is "outside").
	var darkID [GridWidth][GridHeight]int
	var darkTouchesBorder []bool
	queue := make([][2]int, 0, 512)
	nextID := 1
	for sx := 1; sx < GridWidth-1; sx++ {
		for sy := 1; sy < GridHeight-1; sy++ {
			if darkID[sx][sy] != 0 || g.grid[sx][sy].Light >= wall {
				continue
			}
			darkID[sx][sy] = nextID
			queue = append(queue[:0], [2]int{sx, sy})
			touches := false
			for len(queue) > 0 {
				p := queue[0]
				queue = queue[1:]
				if p[0] == 1 || p[0] == GridWidth-2 || p[1] == 1 || p[1] == GridHeight-2 {
					touches = true
				}
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p[0]+d[0], p[1]+d[1]
					if nx < 1 || nx >= GridWidth-1 || ny < 1 || ny >= GridHeight-1 {
						continue
					}
					if darkID[nx][ny] != 0 || g.grid[nx][ny].Light >= wall {
						continue
					}
					darkID[nx][ny] = nextID
					queue = append(queue, [2]int{nx, ny})
				}
			}
			darkTouchesBorder = append(darkTouchesBorder, touches)
			nextID++
		}
	}

	// Pass 2: 4-connected components of claimed cells. A component is
	// "breached" if any of its cells has a 4-neighbor sitting in an
	// outside dark component — meaning the pocket has been reconnected
	// to the border.
	var claimID [GridWidth][GridHeight]int
	var breached []bool
	nextID = 1
	for sx := 0; sx < GridWidth; sx++ {
		for sy := 0; sy < GridHeight; sy++ {
			if claimID[sx][sy] != 0 || !g.grid[sx][sy].Claimed {
				continue
			}
			claimID[sx][sy] = nextID
			queue = append(queue[:0], [2]int{sx, sy})
			open := false
			for len(queue) > 0 {
				p := queue[0]
				queue = queue[1:]
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p[0]+d[0], p[1]+d[1]
					if nx < 0 || nx >= GridWidth || ny < 0 || ny >= GridHeight {
						continue
					}
					if id := darkID[nx][ny]; id != 0 && darkTouchesBorder[id-1] {
						open = true
					}
					if !g.grid[nx][ny].Claimed || claimID[nx][ny] != 0 {
						continue
					}
					claimID[nx][ny] = nextID
					queue = append(queue, [2]int{nx, ny})
				}
			}
			breached = append(breached, open)
			nextID++
		}
	}

	// Pass 3: collapse breached components back to darkness.
	for x := 0; x < GridWidth; x++ {
		for y := 0; y < GridHeight; y++ {
			id := claimID[x][y]
			if id == 0 || !breached[id-1] {
				continue
			}
			c := &g.grid[x][y]
			c.Light = 0
			c.Claimed = false
			c.R, c.G, c.B = 0, 0, 0
		}
	}
}

// isAnchoring reports whether enemies are currently locked in place for a
// bind attempt (phase 1 or 2 on a bind-enabled, non-boss stage).
func (g *Game) isAnchoring() bool {
	s := stages[g.currentStage]
	if !s.EnableBind || s.Boss {
		return false
	}
	return g.bindPhase != 0
}

// advanceBindPhase runs the shared Roaming -> Warning -> Holding -> seal
// loop for the current stage. All enemies share a single timer so the
// "everyone freezes at once" choreography is predictable. When the seal
// frame lands, bindEnclosure runs and enemies are kicked back into motion
// with fresh randomized velocities so the next patrol doesn't replay the
// same trajectory.
func (g *Game) advanceBindPhase() {
	s := stages[g.currentStage]
	if !s.EnableBind || s.Boss {
		g.bindPhase = 0
		g.bindPhaseFrames = 0
		return
	}
	if len(g.enemies) < 2 {
		// A solo enemy can't form a polygon — fall back to plain patrol.
		g.bindPhase = 0
		g.bindPhaseFrames = 0
		if len(g.severedPairs) > 0 {
			g.severedPairs = map[[2]int]bool{}
		}
		return
	}
	g.bindPhaseFrames++
	switch g.bindPhase {
	case 0: // Roaming
		if g.bindPhaseFrames >= BindRoamFrames {
			g.bindPhase = 1
			g.bindPhaseFrames = 0
			for _, e := range g.enemies {
				e.VX, e.VY = 0, 0
				e.HasTarget = false
			}
		}
	case 1: // Warning (pulse only, no line yet)
		if g.bindPhaseFrames >= BindWarnFrames {
			g.bindPhase = 2
			g.bindPhaseFrames = 0
		}
	case 2: // Holding (dark line grows, seal at end)
		if g.bindPhaseFrames >= BindHoldFrames {
			g.bindEnclosure()
			g.bindPhase = 0
			g.bindPhaseFrames = 0
			g.severedPairs = map[[2]int]bool{}
			for _, e := range g.enemies {
				a := g.rng.Float64() * math.Pi * 2
				e.VX = math.Cos(a) * e.Speed
				e.VY = math.Sin(a) * e.Speed
			}
		}
	}
}

// bindEdgesNow returns the active vertex pairs for the current bind attempt.
// Pairs are skipped if either endpoint is too far apart or the pair has
// been severed by a player slash. Returns nil during Roaming.
func (g *Game) bindEdgesNow() [][2]*Enemy {
	if !g.isAnchoring() {
		return nil
	}
	maxPx := float64(BindRangeCells * CellSize)
	var pairs [][2]*Enemy
	for i := 0; i < len(g.enemies); i++ {
		a := g.enemies[i]
		if a.IsBoss {
			continue
		}
		for j := i + 1; j < len(g.enemies); j++ {
			b := g.enemies[j]
			if b.IsBoss {
				continue
			}
			if math.Hypot(a.X-b.X, a.Y-b.Y) > maxPx {
				continue
			}
			if g.severedPairs[pairKey(a.ID, b.ID)] {
				continue
			}
			pairs = append(pairs, [2]*Enemy{a, b})
		}
	}
	return pairs
}

func pairKey(a, b int) [2]int {
	if a < b {
		return [2]int{a, b}
	}
	return [2]int{b, a}
}

// bindEnclosure is the mirror of claimEnclosure: completed bind edges plus
// already-dark cells form the wall set, and any small lit pocket that gets
// cut off from the largest lit region is dropped to Light=0.
func (g *Game) bindEnclosure() {
	edges := g.bindEdgesNow()
	if len(edges) == 0 {
		return
	}
	const wall = WallLightThreshold
	var dark [GridWidth][GridHeight]bool
	for x := 1; x < GridWidth-1; x++ {
		for y := 1; y < GridHeight-1; y++ {
			if g.grid[x][y].Light < wall {
				dark[x][y] = true
			}
			// Cells outside the magic circle act as walls for bind
			// analysis, mirroring claimEnclosure's rule. This stops
			// the player from "tanking" bind by extending a paint
			// bridge from inside to outside the circle, which would
			// otherwise keep every inner lit pocket connected to a
			// vast outer pool and make the largest-component test
			// trivially safe.
			if cellOutsideMagicCircle(x, y) {
				dark[x][y] = true
			}
		}
	}
	for _, pair := range edges {
		g.rasterizeBindLine(pair[0].X, pair[0].Y, pair[1].X, pair[1].Y, &dark)
	}

	var compID [GridWidth][GridHeight]int
	var compSizes []int
	nextID := 1
	queue := make([][2]int, 0, 512)
	for sx := 1; sx < GridWidth-1; sx++ {
		for sy := 1; sy < GridHeight-1; sy++ {
			if compID[sx][sy] != 0 || dark[sx][sy] {
				continue
			}
			compID[sx][sy] = nextID
			queue = append(queue[:0], [2]int{sx, sy})
			size := 0
			for len(queue) > 0 {
				p := queue[0]
				queue = queue[1:]
				size++
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := p[0]+d[0], p[1]+d[1]
					if nx < 1 || nx >= GridWidth-1 || ny < 1 || ny >= GridHeight-1 {
						continue
					}
					if compID[nx][ny] != 0 || dark[nx][ny] {
						continue
					}
					compID[nx][ny] = nextID
					queue = append(queue, [2]int{nx, ny})
				}
			}
			compSizes = append(compSizes, size)
			nextID++
		}
	}
	if len(compSizes) <= 1 {
		return // the lit area wasn't actually severed
	}
	largestID, largestSize := 1, compSizes[0]
	for i, s := range compSizes {
		if s > largestSize {
			largestID = i + 1
			largestSize = s
		}
	}
	for x := 1; x < GridWidth-1; x++ {
		for y := 1; y < GridHeight-1; y++ {
			id := compID[x][y]
			if id == 0 || id == largestID {
				continue
			}
			c := &g.grid[x][y]
			c.Light = 0
			c.Claimed = false
		}
	}
}

// beginKaleidoscope snapshots the grid at the moment of stage clear and
// arms the animation: subsequent tickKaleidoscope() calls will stamp one
// rotated copy at a time onto g.grid. Sampling from a frozen snapshot
// (not from g.grid itself) keeps each fold independent — otherwise fold
// 2 would pick up fold 1's freshly-stamped paint and the brightness
// would cascade unpredictably around the circle.
func (g *Game) beginKaleidoscope() {
	g.kaleidoSnapshot = g.grid
	g.kaleidoNextFold = 1 // fold 0 is the original, already in g.grid
	g.kaleidoFoldTimer = 0
}

// tickKaleidoscope advances the stage-clear animation by one frame. Once
// per KaleidoFoldFrames it stamps the next dihedral transform from the
// snapshot onto g.grid, then increments kaleidoNextFold. When
// kaleidoNextFold reaches KaleidoscopeFolds the animation is done and
// subsequent calls are no-ops until the next stage's beginKaleidoscope.
//
// Steps map directly to dihedralTransform indices (1..7). Order chosen
// for visual growth: 180° opposite first, then the two axis mirrors,
// then the 90° rotation pair, then the diagonal mirror pair.
func (g *Game) tickKaleidoscope() {
	if g.kaleidoNextFold == 0 || g.kaleidoNextFold >= KaleidoscopeFolds {
		return
	}
	g.kaleidoFoldTimer++
	if g.kaleidoFoldTimer < KaleidoFoldFrames {
		return
	}
	g.kaleidoFoldTimer = 0
	g.applyKaleidoscopeFold(g.kaleidoNextFold)
	g.kaleidoNextFold++
}

// applyKaleidoscopeFold stamps one of the dihedral-4 group's
// transformations (idx 1..7; idx 0 is the identity, already in g.grid at
// snapshot time) of g.kaleidoSnapshot onto g.grid, taking max-Light per
// cell so already-bright cells aren't dimmed by a dark sample. All 8
// transforms are integer cell-coordinate operations (see
// dihedralTransform), so the final mandala has exact 8-fold dihedral
// symmetry within the inscribed rectangle. Cells whose transformed
// source falls outside the playfield (rectangular-screen corners that
// would have to come from off-screen under 90° rotation) are skipped.
func (g *Game) applyKaleidoscopeFold(idx int) {
	for x := 0; x < GridWidth; x++ {
		for y := 0; y < GridHeight; y++ {
			sx, sy := dihedralTransform(idx, x, y)
			if sx < 0 || sx >= GridWidth || sy < 0 || sy >= GridHeight {
				continue
			}
			cand := g.kaleidoSnapshot[sx][sy]
			if cand.Light > g.grid[x][y].Light {
				g.grid[x][y] = cand
			}
		}
	}
}

// dihedralTransform returns the source cell (sa, sb) that output cell
// (a, b) inherits its value from under dihedral-4 transform idx. The
// rotation center is the magic-circle center, which sits exactly on the
// (40, 30) cell corner — so all 8 transforms are exact integer cell
// remappings with no quantization.
//
// Indices:
//   0: identity
//   1: 180° rotation around center
//   2: vertical mirror (left↔right across the centerline)
//   3: horizontal mirror (top↔bottom across the centerline)
//   4: 90° CCW rotation
//   5: 90° CW rotation
//   6: diagonal mirror across y=x (through center)
//   7: diagonal mirror across y=-x (through center)
//
// 90° rotations and diagonal mirrors mix the W and H halves of the
// rectangular grid; for cells outside the inscribed square the
// transformed coordinate goes out of range and applyKaleidoscopeFold
// skips them. The magic circle (radius 22 cells) lies fully inside the
// inscribed square, so the visible mandala region is always covered.
func dihedralTransform(idx, a, b int) (int, int) {
	switch idx {
	case 1: // 180° rotation
		return GridWidth - 1 - a, GridHeight - 1 - b
	case 2: // vertical mirror
		return GridWidth - 1 - a, b
	case 3: // horizontal mirror
		return a, GridHeight - 1 - b
	case 4: // 90° CCW rotation
		return GridWidth/2 + GridHeight/2 - 1 - b, a - (GridWidth/2 - GridHeight/2)
	case 5: // 90° CW rotation
		return GridWidth/2 - GridHeight/2 + b, GridWidth/2 + GridHeight/2 - 1 - a
	case 6: // diagonal mirror across y=x
		return GridWidth/2 - GridHeight/2 + b, a - (GridWidth/2 - GridHeight/2)
	case 7: // diagonal mirror across y=-x
		return GridWidth/2 + GridHeight/2 - 1 - b, GridWidth/2 + GridHeight/2 - 1 - a
	}
	return a, b // idx 0 or unknown: identity
}

func (g *Game) rasterizeBindLine(x0, y0, x1, y1 float64, dark *[GridWidth][GridHeight]bool) {
	length := math.Hypot(x1-x0, y1-y0)
	steps := int(length) + 1
	for i := 0; i <= steps; i++ {
		u := float64(i) / float64(steps)
		px := x0 + (x1-x0)*u
		py := y0 + (y1-y0)*u
		cx := int(px) / CellSize
		cy := int(py) / CellSize
		for ddx := -BindEdgeWidthCells; ddx <= BindEdgeWidthCells; ddx++ {
			for ddy := -BindEdgeWidthCells; ddy <= BindEdgeWidthCells; ddy++ {
				x := cx + ddx
				y := cy + ddy
				if x < 1 || x >= GridWidth-1 || y < 1 || y >= GridHeight-1 {
					continue
				}
				if ddx*ddx+ddy*ddy > BindEdgeWidthCells*BindEdgeWidthCells {
					continue
				}
				dark[x][y] = true
			}
		}
	}
}

// repositionTutorialFoeAwayFrom moves the stage-1 foe to a slot right
// alongside the first beam — close enough that the player can clearly
// see "I drew that, the foe sat down next to it" but offset on the
// perpendicular so the beam itself doesn't hide it. Called the frame
// the first slash flies, before any lighting reaches the foe, so the
// teleport itself is invisible. The two perpendicular candidates (one
// on each side of the beam) are compared; the one that fits safely
// inside the playfield is preferred, and final clamp keeps the foe
// from sliding off-screen if the beam ran along an edge.
func (g *Game) repositionTutorialFoeAwayFrom(x0, y0, x1, y1 float64) {
	if len(g.enemies) == 0 {
		return
	}
	const offset = 90.0 // px from the beam line — close enough to feel grouped
	const margin = 60.0 // px playfield safe area for the foe center
	midX := (x0 + x1) / 2
	midY := (y0 + y1) / 2
	length := math.Hypot(x1-x0, y1-y0)
	if length < 1 {
		return
	}
	perpX := -(y1 - y0) / length
	perpY := (x1 - x0) / length
	candA := [2]float64{midX + perpX*offset, midY + perpY*offset}
	candB := [2]float64{midX - perpX*offset, midY - perpY*offset}
	inSafe := func(p [2]float64) bool {
		return p[0] >= margin && p[0] <= ScreenWidth-margin &&
			p[1] >= margin && p[1] <= ScreenHeight-margin
	}
	var chosen [2]float64
	switch {
	case inSafe(candA) && !inSafe(candB):
		chosen = candA
	case inSafe(candB) && !inSafe(candA):
		chosen = candB
	default:
		// Both fit (or neither): take the one closer to the screen
		// center so the foe stays in the natural focal area.
		cx, cy := float64(ScreenWidth)/2, float64(ScreenHeight)/2
		dA := math.Hypot(candA[0]-cx, candA[1]-cy)
		dB := math.Hypot(candB[0]-cx, candB[1]-cy)
		if dA <= dB {
			chosen = candA
		} else {
			chosen = candB
		}
	}
	if chosen[0] < margin {
		chosen[0] = margin
	}
	if chosen[0] > ScreenWidth-margin {
		chosen[0] = ScreenWidth - margin
	}
	if chosen[1] < margin {
		chosen[1] = margin
	}
	if chosen[1] > ScreenHeight-margin {
		chosen[1] = ScreenHeight - margin
	}
	e := g.enemies[0]
	e.X, e.Y = chosen[0], chosen[1]
}

// pointSegmentDistance is the shortest distance from (px,py) to the segment
// (x0,y0)-(x1,y1). Used for "did the slash beam touch this anchored enemy."
func pointSegmentDistance(px, py, x0, y0, x1, y1 float64) float64 {
	dx := x1 - x0
	dy := y1 - y0
	if dx == 0 && dy == 0 {
		return math.Hypot(px-x0, py-y0)
	}
	t := ((px-x0)*dx + (py-y0)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	cx := x0 + t*dx
	cy := y0 + t*dy
	return math.Hypot(px-cx, py-cy)
}

// segmentsIntersect tests strict (non-collinear) intersection between two
// segments. Used to detect "did the slash cut this dark line".
func segmentsIntersect(ax, ay, bx, by, cx, cy, dxp, dyp float64) bool {
	d1 := cross2(dxp-cx, dyp-cy, ax-cx, ay-cy)
	d2 := cross2(dxp-cx, dyp-cy, bx-cx, by-cy)
	d3 := cross2(bx-ax, by-ay, cx-ax, cy-ay)
	d4 := cross2(bx-ax, by-ay, dxp-ax, dyp-ay)
	return ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))
}

func cross2(ax, ay, bx, by float64) float64 {
	return ax*by - ay*bx
}

// cellOutsideMagicCircle reports whether a grid cell's center sits outside
// the magic circle. claimEnclosure treats such cells as "outside-equivalent"
// so any flood-fill component containing them is excluded from claims —
// otherwise the player could draw a giant triangle wrapping the whole
// screen from outside the circle and sweep every leashed foe in one claim.
func cellOutsideMagicCircle(cx, cy int) bool {
	px := cx*CellSize + CellSize/2 - MagicCircleCX
	py := cy*CellSize + CellSize/2 - MagicCircleCY
	return px*px+py*py > MagicCircleRadiusPx*MagicCircleRadiusPx
}

// clipSegmentToCircle clips a slash segment to the magic-circle interior.
// The seal mechanically contains the rite: only the inside portion of any
// stroke can paint cells or land a claim. Returns ok=false when the segment
// lies entirely outside the circle (no intersection at all), so callers
// know to refund energy or hide the ghost preview rather than show a beam
// that wouldn't actually do anything.
func clipSegmentToCircle(x0, y0, x1, y1 float64) (rx0, ry0, rx1, ry1 float64, ok bool) {
	cx := float64(MagicCircleCX)
	cy := float64(MagicCircleCY)
	r := float64(MagicCircleRadiusPx)
	dx := x1 - x0
	dy := y1 - y0
	a := dx*dx + dy*dy
	if a < 1e-9 {
		// Single point: inside iff within radius.
		ex := x0 - cx
		ey := y0 - cy
		if ex*ex+ey*ey > r*r {
			return 0, 0, 0, 0, false
		}
		return x0, y0, x0, y0, true
	}
	fx := x0 - cx
	fy := y0 - cy
	b := 2 * (fx*dx + fy*dy)
	c := fx*fx + fy*fy - r*r
	disc := b*b - 4*a*c
	if disc < 0 {
		return 0, 0, 0, 0, false
	}
	sq := math.Sqrt(disc)
	t0 := (-b - sq) / (2 * a)
	t1 := (-b + sq) / (2 * a)
	if t0 > 1 || t1 < 0 {
		return 0, 0, 0, 0, false
	}
	if t0 < 0 {
		t0 = 0
	}
	if t1 > 1 {
		t1 = 1
	}
	return x0 + dx*t0, y0 + dy*t0, x0 + dx*t1, y0 + dy*t1, true
}

func hueToRGB(h float64) (float32, float32, float32) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := 1.0
	x := 1 - math.Abs(math.Mod(h/60.0, 2)-1)
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	// Soften the fully-saturated HSV output toward white. Pure HSV
	// primaries read as harsh on the dark background; mixing in some
	// white pushes the palette toward stained-glass pastels while
	// preserving hue separation.
	s := PaletteSoftness
	r = r*(1-s) + s
	g = g*(1-s) + s
	b = b*(1-s) + s
	return float32(r), float32(g), float32(b)
}

func (g *Game) Draw(screen *ebiten.Image) {
	if g.gridImg == nil {
		g.gridImg = ebiten.NewImage(GridWidth, GridHeight)
	}
	for y := 0; y < GridHeight; y++ {
		for x := 0; x < GridWidth; x++ {
			c := g.grid[x][y]
			i := (y*GridWidth + x) * 4
			g.pixels[i+0] = byte(clamp01(c.R*c.Light) * 255)
			g.pixels[i+1] = byte(clamp01(c.G*c.Light) * 255)
			g.pixels[i+2] = byte(clamp01(c.B*c.Light) * 255)
			g.pixels[i+3] = 255
		}
	}
	g.gridImg.WritePixels(g.pixels)
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(CellSize, CellSize)
	screen.DrawImage(g.gridImg, op)

	// Background flavor: the magic circle is the "outer perimeter seal,"
	// the still-living ward from previous keepers. The player's light is
	// the inner pattern being relit; the foes are leashed inside the rim.
	// Drawn after the grid with low alpha — bright cells overdraw it where
	// they shine, so it reads only against the dark.
	mcx, mcy := float32(MagicCircleCX), float32(MagicCircleCY)
	mcr := float32(MagicCircleRadiusPx)
	vector.StrokeCircle(screen, mcx, mcy, mcr, 1.5, color.RGBA{100, 78, 42, 130}, true)
	vector.StrokeCircle(screen, mcx, mcy, mcr*0.72, 1, color.RGBA{70, 55, 30, 90}, true)

	// Lesser seals in the dead space — vestiges of older wards that
	// have lost their power. Pure decoration; tells the story that this
	// is the "next attempt" in a longer line of keepers.
	lesserA := color.RGBA{55, 42, 22, 110}
	for _, q := range [4][2]float32{
		{ScreenWidth * 0.10, ScreenHeight * 0.18},
		{ScreenWidth * 0.90, ScreenHeight * 0.18},
		{ScreenWidth * 0.10, ScreenHeight * 0.82},
		{ScreenWidth * 0.90, ScreenHeight * 0.82},
	} {
		vector.StrokeCircle(screen, q[0], q[1], 18, 1, lesserA, true)
		vector.StrokeCircle(screen, q[0], q[1], 9, 1, lesserA, true)
	}

	// Rim flash where a foe pushed against the seal. Drawn under the
	// enemies so the contact point reads as the seal reacting, not as
	// an enemy attribute.
	for _, e := range g.enemies {
		if e.IsBoss || e.EdgeGlow < 0.05 {
			continue
		}
		dxc := e.X - float64(MagicCircleCX)
		dyc := e.Y - float64(MagicCircleCY)
		dc := math.Hypot(dxc, dyc)
		if dc < 1 {
			continue
		}
		px := float64(MagicCircleCX) + dxc*float64(MagicCircleRadiusPx)/dc
		py := float64(MagicCircleCY) + dyc*float64(MagicCircleRadiusPx)/dc
		a := uint8(200 * e.EdgeGlow)
		vector.DrawFilledCircle(screen, float32(px), float32(py), 11,
			color.RGBA{180, 130, 60, a}, true)
	}

	// Always-lit border frame: visualizes the 1-cell ring that
	// claimEnclosure treats as permanent wall. Drawn 1 cell thick so the
	// visible frame matches the claim geometry exactly.
	frame := color.RGBA{180, 200, 235, 210}
	vector.DrawFilledRect(screen, 0, 0, ScreenWidth, CellSize, frame, false)
	vector.DrawFilledRect(screen, 0, ScreenHeight-CellSize, ScreenWidth, CellSize, frame, false)
	vector.DrawFilledRect(screen, 0, 0, CellSize, ScreenHeight, frame, false)
	vector.DrawFilledRect(screen, ScreenWidth-CellSize, 0, CellSize, ScreenHeight, frame, false)

	// Anchor pulse: drawn UNDER the enemies so the vertex circle stays
	// visually crisp on top. Pulses faster as the seal approaches, and the
	// Warning phase uses a smaller/dimmer ring so the player can tell
	// "they're about to commit" vs "the line is forming now."
	if g.isAnchoring() {
		pulse := 0.5 + 0.5*math.Sin(float64(g.bindPhaseFrames)*0.2)
		scale := float32(1.0)
		if g.bindPhase == 1 {
			scale = 0.6
		}
		auraR := (24 + float32(pulse)*10) * scale
		auraA := uint8((50 + pulse*60) * float64(scale))
		for _, e := range g.enemies {
			if e.IsBoss {
				continue
			}
			vector.DrawFilledCircle(screen, float32(e.X), float32(e.Y), auraR,
				color.RGBA{70, 25, 90, auraA}, true)
		}
	}

	// Lighting model: scan the whole grid for lit cells with linear falloff
	// (so a slash anywhere on screen reaches every enemy). The weighted
	// centroid gives a "light direction" used for Lambertian shading, but
	// we also split the energy into ambient (omnidirectional) and
	// directional based on how concentrated the light is — measured by
	// |centroidVector| / (totalLight * avgDistance). A single slash gives
	// a strong directional with no ambient (lit hemisphere only); slashes
	// surrounding the enemy give strong ambient with weak directional
	// (the whole sphere reads). brightness is anchored on the strongest
	// single cell contribution so any light on screen guarantees the
	// enemy becomes visible at least faintly.
	const lightMaxDist = 100.0 // cells; covers the screen diagonal
	for _, e := range g.enemies {
		cx0 := int(e.X) / CellSize
		cy0 := int(e.Y) / CellSize
		var totalLight, totalDistance, dirX, dirY float32
		var litR, litG, litB float32
		var maxContribution float32
		for x := 0; x < GridWidth; x++ {
			for y := 0; y < GridHeight; y++ {
				c := g.grid[x][y]
				if c.Light <= 0 {
					continue
				}
				dx := x - cx0
				dy := y - cy0
				d2 := dx*dx + dy*dy
				if d2 == 0 {
					continue
				}
				d := float32(math.Sqrt(float64(d2)))
				if d > lightMaxDist {
					continue
				}
				falloff := 1 - d/lightMaxDist
				w := c.Light * falloff
				totalLight += w
				totalDistance += d * w
				dirX += float32(dx) * w
				dirY += float32(dy) * w
				litR += c.R * w
				litG += c.G * w
				litB += c.B * w
				if w > maxContribution {
					maxContribution = w
				}
			}
		}
		if totalLight <= 0 {
			continue
		}
		brightness := maxContribution
		if brightness > 1 {
			brightness = 1
		}
		dirLen := float32(math.Hypot(float64(dirX), float64(dirY)))
		avgD := totalDistance / totalLight
		// asymmetry: 1 when light is concentrated in one direction,
		// 0 when sources surround the enemy symmetrically.
		asymmetry := float32(0)
		if avgD > 0.01 {
			asymmetry = dirLen / (totalLight * avgD)
			if asymmetry > 1 {
				asymmetry = 1
			}
		}
		var lnx, lny, lnz float32
		if dirLen > 0.01 {
			lx2D := dirX / dirLen
			ly2D := dirY / dirLen
			const lz = 0.5
			lvlen := float32(math.Sqrt(float64(lx2D*lx2D+ly2D*ly2D) + lz*lz))
			lnx = lx2D / lvlen
			lny = ly2D / lvlen
			lnz = float32(lz) / lvlen
		}
		sr := clamp01(litR/totalLight + 0.2)
		sg := clamp01(litG/totalLight + 0.2)
		sb := clamp01(litB/totalLight + 0.25)

		ambient := brightness * (1 - asymmetry) * 0.85
		directional := brightness * asymmetry

		radius := float32(e.Radius)
		stepPx := float32(1.5)
		if e.IsBoss {
			stepPx = 3.0
		}
		half := int(radius/stepPx) + 1
		rectSize := stepPx * 1.4
		r2 := radius * radius
		for ix := -half; ix <= half; ix++ {
			for iy := -half; iy <= half; iy++ {
				sx := float32(ix) * stepPx
				sy := float32(iy) * stepPx
				d2f := sx*sx + sy*sy
				if d2f > r2 {
					continue
				}
				sz := float32(math.Sqrt(float64(r2 - d2f)))
				nx := sx / radius
				ny := sy / radius
				nz := sz / radius
				var lambert float32
				if dirLen > 0.01 {
					lambert = nx*lnx + ny*lny + nz*lnz
					if lambert < 0 {
						lambert = 0
					}
				}
				intensity := ambient + directional*lambert
				if intensity < 0.04 {
					continue
				}
				vector.DrawFilledRect(screen,
					float32(e.X)+sx-rectSize/2,
					float32(e.Y)+sy-rectSize/2,
					rectSize, rectSize,
					color.RGBA{
						uint8(clamp01(intensity*sr) * 255),
						uint8(clamp01(intensity*sg) * 255),
						uint8(clamp01(intensity*sb) * 255),
						uint8(clamp01(intensity*1.5) * 220),
					}, true)
			}
		}
	}

	// Dark lines: only visible during phase 2 (Holding), and they grow from
	// the midpoint outward over BindHoldFrames. Drawn over enemies so the
	// player sees the line connecting vertices, but under slashes so the
	// player's white beam reads as the dominant action.
	if g.isAnchoring() && g.bindPhase == 2 {
		growthT := float64(g.bindPhaseFrames) / float64(BindHoldFrames)
		if growthT > 1 {
			growthT = 1
		}
		alpha := uint8(140 + growthT*100)
		for _, pair := range g.bindEdgesNow() {
			a, b := pair[0], pair[1]
			midX := float32((a.X + b.X) / 2)
			midY := float32((a.Y + b.Y) / 2)
			ax := float32(a.X) - midX
			ay := float32(a.Y) - midY
			bx := float32(b.X) - midX
			by := float32(b.Y) - midY
			x0 := midX + ax*float32(growthT)
			y0 := midY + ay*float32(growthT)
			x1 := midX + bx*float32(growthT)
			y1 := midY + by*float32(growthT)
			vector.StrokeLine(screen, x0, y0, x1, y1, 2.5,
				color.RGBA{40, 8, 55, alpha}, true)
		}
	}

	// Active slashes: the beam extends from the start point toward the tip,
	// then fades. With SlashRevealFrames=1 the line snaps to full length on
	// the first drawn frame; the formula still uses t so a longer Reveal
	// would animate the tip outward. A brief shockwave ring at the start
	// point sells the "swing began here" beat of the strike.
	for _, sl := range g.slashes {
		t := float64(sl.Frame) / float64(SlashRevealFrames)
		if t > 1 {
			t = 1
		}
		startX := float32(sl.X0)
		startY := float32(sl.Y0)
		tipX := float32(sl.X0 + (sl.X1-sl.X0)*t)
		tipY := float32(sl.Y0 + (sl.Y1-sl.Y0)*t)

		var alpha uint8 = 240
		width := float32(4)
		if sl.Frame > SlashRevealFrames {
			gp := float64(sl.Frame-SlashRevealFrames) / float64(SlashGlowFrames)
			if gp > 1 {
				gp = 1
			}
			alpha = uint8(220 * (1 - gp))
			width = 4 - 2.5*float32(gp)
		}
		vector.StrokeLine(screen, startX, startY, tipX, tipY,
			width, color.RGBA{255, 255, 255, alpha}, true)

		// Shockwave ring at the start point: widens and fades over the
		// first few frames.
		if sl.Frame <= 4 {
			r := float32(6 + sl.Frame*7)
			a := uint8(150 - sl.Frame*30)
			vector.StrokeCircle(screen, startX, startY, r, 2,
				color.RGBA{255, 255, 255, a}, true)
		}

		// Energy-shortfall tail: red dashes continuing past the actual
		// tip, showing the beam the player asked for but couldn't afford.
		// Brief and loud so a fast-drag player notices it at release time
		// (where the eye already is) without needing to track the ghost.
		if sl.HasUnfired && sl.Frame < UnfiredTailFrames {
			t := 1 - float64(sl.Frame)/float64(UnfiredTailFrames)
			tailA := uint8(220 * t)
			udx := sl.UnfiredToX - sl.UnfiredFromX
			udy := sl.UnfiredToY - sl.UnfiredFromY
			const dashes = 4
			const on = 0.6 // fraction of each dash slot that's drawn
			for i := 0; i < dashes; i++ {
				t0 := float64(i) / float64(dashes)
				t1 := t0 + on/float64(dashes)
				ax := sl.UnfiredFromX + udx*t0
				ay := sl.UnfiredFromY + udy*t0
				bx := sl.UnfiredFromX + udx*t1
				by := sl.UnfiredFromY + udy*t1
				vector.StrokeLine(screen, float32(ax), float32(ay),
					float32(bx), float32(by), 2.5,
					color.RGBA{255, 70, 60, tailA}, true)
			}
		}
	}

	// Drag preview. The slash starts at the drag start point and extends in
	// the drag direction for the length matched to the unit bucket. Shows
	// three things simultaneously:
	//   - the actual drag segment (start -> current pointer) as a small bright
	//     line, so the player feels the strike forming under their finger;
	//   - a ghost of the slash that would fire right now (start-anchored,
	//     length matched to the unit bucket the drag falls into) — its tip
	//     pokes past the pointer for any drag within its bucket's max, so the
	//     player can see what they're aiming;
	//   - a start-point ring whose radius/intensity scales with the unit count
	//     so "this is a 3-unit haymaker" reads at a glance vs "this is a
	//     1-unit dab." The ring sits at the strike origin (= the drag's first
	//     touch, well away from the moving fingertip) for maximum visibility.
	if g.state == StatePlaying && g.dragging {
		sx := float32(g.slashStartX)
		sy := float32(g.slashStartY)
		px := float32(g.pointerX)
		py := float32(g.pointerY)
		dxp := float64(px - sx)
		dyp := float64(py - sy)
		dist := math.Hypot(dxp, dyp)

		if units, length, ok := slashSpec(dist, g.stock); ok {
			nx := dxp / dist
			ny := dyp / dist
			gx1raw := float64(sx) + nx*length
			gy1raw := float64(sy) + ny*length

			// Mirror fireSlash's clip so the ghost shows exactly what the
			// beam will become. If the stroke would land entirely outside
			// the seal the ghost (and ring) hide — the player sees only
			// the bright drag indicator and learns "aim inside the circle."
			cx0, cy0, cx1, cy1, inside := clipSegmentToCircle(float64(sx), float64(sy), gx1raw, gy1raw)
			if inside {
				var ghostWidth float32
				var ghostAlpha uint8
				var ringR float32
				switch units {
				case 1:
					ghostWidth, ghostAlpha, ringR = 1.0, 90, 4
				case 2:
					ghostWidth, ghostAlpha, ringR = 1.8, 140, 7
				case 3:
					ghostWidth, ghostAlpha, ringR = 2.6, 210, 11
				}
				vector.StrokeLine(screen, float32(cx0), float32(cy0), float32(cx1), float32(cy1), ghostWidth,
					color.RGBA{255, 255, 255, ghostAlpha}, true)
				// Ring sits at the slash's effective origin (the clipped
				// start), not necessarily where the finger first landed,
				// so the player reads "this is where the beam begins."
				vector.StrokeCircle(screen, float32(cx0), float32(cy0), ringR, 1.5,
					color.RGBA{255, 255, 255, ghostAlpha}, true)
			}
		}
		vector.StrokeLine(screen, sx, sy, px, py, 2.5,
			color.RGBA{255, 255, 255, 220}, true)
		vector.DrawFilledCircle(screen, sx, sy, 3, color.RGBA{255, 255, 255, 230}, true)
	}

	// Energy pips + trickle progress bar. Filled pip = unit ready to spend;
	// hollow pip = not yet regenerated. The bar shows progress toward the
	// *next* unit (not toward a full magazine), so it's always animating
	// while the player isn't capped — a steady visible drumbeat of recharge.
	pipY := float32(14)
	pipR := float32(5)
	pipGap := float32(16)
	// Pip-flash band: a translucent red strip behind the just-consumed
	// pips. Drawn first so it sits under the pip glyphs. The expanding
	// per-pip ring (below) adds a second peripheral cue — together they
	// give a fast-drag player something to notice without needing to
	// stare at the HUD.
	if g.pipFlashFrames > 0 && g.pipFlashCount > 0 {
		t := float32(g.pipFlashFrames) / float32(PipFlashFrames)
		bandX := 14 + float32(g.pipFlashFromIdx)*pipGap - pipGap/2
		bandW := float32(g.pipFlashCount) * pipGap
		vector.DrawFilledRect(screen, bandX, pipY-pipR-5, bandW, pipR*2+10,
			color.RGBA{255, 60, 40, uint8(140 * t)}, false)
	}
	for i := 0; i < MaxStock; i++ {
		cx := 14 + float32(i)*pipGap
		if i < g.stock {
			vector.DrawFilledCircle(screen, cx, pipY, pipR,
				color.RGBA{200, 230, 255, 240}, true)
		} else {
			vector.StrokeCircle(screen, cx, pipY, pipR, 1.5,
				color.RGBA{120, 140, 160, 220}, true)
		}
		if g.pipFlashFrames > 0 && i >= g.pipFlashFromIdx && i < g.pipFlashFromIdx+g.pipFlashCount {
			t := float32(g.pipFlashFrames) / float32(PipFlashFrames)
			ringR := pipR + 3 + 4*t
			vector.StrokeCircle(screen, cx, pipY, ringR, 2,
				color.RGBA{255, 100, 80, uint8(230 * t)}, true)
		}
	}
	if g.stock < MaxStock {
		barX := 14 + float32(MaxStock)*pipGap + 4
		barY := pipY - 3
		barW := float32(60)
		vector.DrawFilledRect(screen, barX, barY, barW, 6,
			color.RGBA{40, 40, 50, 220}, false)
		vector.DrawFilledRect(screen, barX, barY, barW*float32(g.reloadProgress), 6,
			color.RGBA{200, 230, 255, 200}, false)
	}

	// NRGBA for text colors: text/v2's glyph alpha compositing breaks down
	// when the source color violates premultiplied-alpha invariants. RGBA is
	// fine when alpha is 255, but as soon as we want a translucent dim or a
	// fade, premultiplied math produces visible glyph artifacts.
	white := color.NRGBA{240, 245, 255, 255}
	dim := color.NRGBA{170, 185, 210, 230}
	gold := color.NRGBA{255, 220, 130, 255}

	// HUD: numbers carry the meaning, labels stay out of the way. Foes is
	// the win-condition counter so it gets the big face and the screen
	// center. Stage anchors top-left as "N/M"; time anchors top-right.
	// Light percent is a faint footer so it doesn't compete with foes.
	g.drawAt(screen, fmt.Sprintf("%d / %d", g.currentStage+1, len(stages)), 10, 36, g.faceMid, dim)
	foesMsg := fmt.Sprintf("%d", len(g.enemies))
	foesW, _ := text.Measure(foesMsg, g.faceLarge, 0)
	g.drawAt(screen, foesMsg, ScreenWidth/2-int(foesW)/2, 18, g.faceLarge, white)
	timeMsg := fmt.Sprintf("%4.1fs", g.stageTime)
	timeW, _ := text.Measure(timeMsg, g.faceMid, 0)
	g.drawAt(screen, timeMsg, ScreenWidth-10-int(timeW), 36, g.faceMid, white)
	g.drawAt(screen, fmt.Sprintf("%3.0f%%", g.lightPercent*100), 10, ScreenHeight-22, g.faceSmall, dim)

	if DebugMode {
		ebitenutil.DebugPrintAt(screen,
			fmt.Sprintf("FPS %4.1f", ebiten.ActualFPS()), ScreenWidth-90, ScreenHeight-18)
	}

	// Stage-1 onboarding hints. Two stages keyed to game events, not
	// stroke count: "DRAG" teaches the input, "ENCLOSE" teaches the goal
	// and persists until the player actually seals a pocket. Three random
	// strokes won't always form a sealable shape, so a stroke-counted
	// hint would leave the player at "1 MORE" with no way forward.
	if g.state == StatePlaying && g.currentStage == 0 && g.tutorialStep < 2 {
		hint := "DRAG"
		if g.tutorialStep == 1 {
			hint = "ENCLOSE"
		}
		g.drawCenterFace(screen, hint, ScreenHeight-60, g.faceLarge, white)
	}

	// SEALED! flash: triggered the frame claimEnclosure removed at least one
	// enemy. Loud, brief, center-screen — sells the "your shape just killed
	// something" causality that the silent enemy disappearance otherwise
	// hides under the slash glow. Gated on StatePlaying so it doesn't stack
	// on top of the CLEAR message when the killing blow also empties the
	// board (same frame: sealedFlashFrames is set AND state flips to
	// StateCleared, and Update stops ticking, so the flash would freeze
	// underneath CLEAR forever).
	if g.sealedFlashFrames > 0 && g.state == StatePlaying {
		alphaT := float64(g.sealedFlashFrames) / 30.0
		if alphaT > 1 {
			alphaT = 1
		}
		c := color.NRGBA{255, 240, 180, uint8(255 * alphaT)}
		g.drawCenterFace(screen, "SEALED!", ScreenHeight/2-20, g.faceLarge, c)
	}

	switch g.state {
	case StateTitle:
		g.drawCenterFace(screen, "RIFT", ScreenHeight/2-70, g.faceLarge, white)
		g.drawCenterFace(screen, "DRAG.  SLASH.  ENCLOSE.  SEAL.", ScreenHeight/2-10, g.faceMid, white)
		g.drawCenterFace(screen, "Click / Space", ScreenHeight/2+50, g.faceSmall, dim)
	case StateCleared:
		g.drawCenterFace(screen, "CLEAR", ScreenHeight/2-20, g.faceLarge, gold)
		// During the cooldown the mandala is still assembling; show what
		// stage is next. Once the cooldown ends we're waiting on the
		// player's click/space, so swap to the input prompt.
		var hint string
		if g.postClearCooldown > 0 {
			if g.currentStage+1 >= len(stages) {
				hint = "Final stage"
			} else {
				hint = fmt.Sprintf("Next: Stage %d", g.currentStage+2)
			}
		} else {
			hint = "Click / Space"
		}
		g.drawCenterFace(screen, hint, ScreenHeight/2+20, g.faceSmall, dim)
	case StateGameOver:
		g.drawCenterFace(screen, "TIME UP", ScreenHeight/2-20, g.faceLarge, color.NRGBA{255, 150, 150, 255})
		g.drawCenterFace(screen, "Click / Space", ScreenHeight/2+20, g.faceSmall, dim)
	case StateAllCleared:
		g.drawCenterFace(screen, "ALL CLEAR", ScreenHeight/2-20, g.faceLarge, gold)
		g.drawCenterFace(screen, "Click / Space", ScreenHeight/2+20, g.faceSmall, dim)
	}
}

// drawCenter horizontally centers msg at the given baseline y using the
// medium face. Kept as the default text path so existing call sites stay
// terse; use drawCenterFace for larger or smaller text.
func (g *Game) drawCenter(screen *ebiten.Image, msg string, y int) {
	g.drawCenterFace(screen, msg, y, g.faceMid, color.NRGBA{230, 240, 255, 255})
}

func (g *Game) drawCenterFace(screen *ebiten.Image, msg string, y int, face *text.GoTextFace, c color.Color) {
	w, _ := text.Measure(msg, face, 0)
	op := &text.DrawOptions{}
	op.GeoM.Translate(float64(ScreenWidth)/2-w/2, float64(y))
	op.ColorScale.ScaleWithColor(c)
	text.Draw(screen, msg, face, op)
}

// drawAt is a thin convenience wrapper that places msg at the given
// top-left pixel with the chosen face and color. Anchor is "top" because
// text/v2 baselines from the top-left when no PrimaryAlign is set.
func (g *Game) drawAt(screen *ebiten.Image, msg string, x, y int, face *text.GoTextFace, c color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(float64(x), float64(y))
	op.ColorScale.ScaleWithColor(c)
	text.Draw(screen, msg, face, op)
}

func (g *Game) Layout(outsideW, outsideH int) (int, int) {
	return ScreenWidth, ScreenHeight
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func main() {
	initDebugMode()
	initAudio()
	ebiten.SetWindowSize(ScreenWidth, ScreenHeight)
	ebiten.SetWindowTitle("Rift")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(newGame()); err != nil {
		log.Fatal(err)
	}
}
