package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
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
	{Enemies: 1, EnemySpeed: 0.20, TimeLimit: 30, HarmlessEnemy: true},   // 1 tutorial: one slow harmless target
	{Enemies: 1, EnemySpeed: 0.40, TimeLimit: 45},                        // 2
	{Enemies: 2, EnemySpeed: 0.40, TimeLimit: 45},                        // 3
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
type Slash struct {
	X0, Y0  float64
	X1, Y1  float64
	Hue     float64
	Frame   int
	claimed bool
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
}

func newGame() *Game {
	g := &Game{
		state:  StateTitle,
		stock:  MaxStock,
		rng:    rand.New(rand.NewSource(20260622)),
		pixels: make([]byte, GridWidth*GridHeight*4),
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
	for i := 0; i < s.Enemies; i++ {
		g.enemies = append(g.enemies, &Enemy{
			ID:           i + 1,
			X:            g.rng.Float64()*float64(ScreenWidth-160) + 80,
			Y:            g.rng.Float64()*float64(ScreenHeight-160) + 80,
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

func (g *Game) Update() error {
	switch g.state {
	case StateTitle:
		if pointerJustPressed() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.loadStage(0)
			g.state = StatePlaying
		}
	case StatePlaying:
		g.updatePlaying()
	case StateCleared:
		if g.postClearCooldown > 0 {
			g.postClearCooldown--
			break
		}
		g.loadStage(g.currentStage + 1)
		if g.state != StateAllCleared {
			g.state = StatePlaying
		}
	case StateGameOver:
		if g.postClearCooldown > 0 {
			g.postClearCooldown--
			break
		}
		if pointerJustPressed() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.loadStage(g.currentStage)
			g.state = StatePlaying
		}
	case StateAllCleared:
		if g.postClearCooldown > 0 {
			g.postClearCooldown--
			break
		}
		if pointerJustPressed() || inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			g.state = StateTitle
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
		g.erodeAround(e.X, e.Y, e.EffectRadius)
	}

	// Claims are not permanent — once the perimeter is chewed through, the
	// pocket reverts. Runs after erosion so the same frame's damage is
	// reflected immediately.
	g.reviewClaims()

	g.stageTime -= 1.0 / 60.0
	if g.stageTime <= 0 {
		g.state = StateGameOver
		g.postClearCooldown = 45
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
		g.postClearCooldown = 60
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
	g.stock -= units
	sx0 := float64(x0)
	sy0 := float64(y0)
	sx1 := sx0 + nx*length
	sy1 := sy0 + ny*length
	g.slashes = append(g.slashes, &Slash{
		X0:  sx0,
		Y0:  sy0,
		X1:  sx1,
		Y1:  sy1,
		Hue: g.hue,
	})

	// Anchored enemies are vulnerable: a slash that grazes them removes the
	// vertex (and any pair that included it stops contributing to the seal).
	// Sever any remaining dark lines that the beam crossed. Both checks are
	// gated on isAnchoring so during normal patrol the slash still only
	// affects light, not enemies.
	if g.isAnchoring() {
		bladePx := float64(SlashHitRadius * CellSize)
		survivors := g.enemies[:0]
		for _, e := range g.enemies {
			if !e.IsBoss && pointSegmentDistance(e.X, e.Y, sx0, sy0, sx1, sy1) <= e.Radius+bladePx {
				continue // sealed/cut: this anchored enemy is removed
			}
			survivors = append(survivors, e)
		}
		g.enemies = survivors
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
// and, whenever both coordinates change in one step, also paint one of the
// two intermediates so the wall stays 4-connected for any BladeRadius.
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
			// Diagonal jump — patch one orthogonal neighbour at this cell's
			// center to preserve 4-connectivity.
			g.illuminate(prevCX*CellSize+CellSize/2, cy*CellSize+CellSize/2)
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
	e.VX = e.VX*(1-steer) + (dx/d)*e.Speed*steer
	e.VY = e.VY*(1-steer) + (dy/d)*e.Speed*steer
	if sp := math.Hypot(e.VX, e.VY); sp > e.Speed {
		e.VX *= e.Speed / sp
		e.VY *= e.Speed / sp
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
// edge" effect.
func (g *Game) erodeAround(x, y, radius float64) {
	cx := int(x) / CellSize
	cy := int(y) / CellSize
	rcell := int(radius/CellSize) + 1
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
		}
	}
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
			gx1 := sx + float32(nx*length)
			gy1 := sy + float32(ny*length)

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
			vector.StrokeLine(screen, sx, sy, gx1, gy1, ghostWidth,
				color.RGBA{255, 255, 255, ghostAlpha}, true)
			vector.StrokeCircle(screen, sx, sy, ringR, 1.5,
				color.RGBA{255, 255, 255, ghostAlpha}, true)
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
	for i := 0; i < MaxStock; i++ {
		cx := 14 + float32(i)*pipGap
		if i < g.stock {
			vector.DrawFilledCircle(screen, cx, pipY, pipR,
				color.RGBA{200, 230, 255, 240}, true)
		} else {
			vector.StrokeCircle(screen, cx, pipY, pipR, 1.5,
				color.RGBA{120, 140, 160, 220}, true)
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

	msg := fmt.Sprintf("Stage %d/%d   Light %3.0f%%   Foes %d   Time %4.1fs",
		g.currentStage+1, len(stages), g.lightPercent*100, len(g.enemies), g.stageTime)
	ebitenutil.DebugPrintAt(screen, msg, 10, 24)

	if DebugMode {
		ebitenutil.DebugPrintAt(screen,
			fmt.Sprintf("FPS %4.1f", ebiten.ActualFPS()), 10, ScreenHeight-18)
	}

	switch g.state {
	case StateTitle:
		drawCenter(screen, "R I F T", ScreenHeight/2-40)
		drawCenter(screen, "Drag a straight slash. Short drag = short beam, long drag = long beam.", ScreenHeight/2-10)
		drawCenter(screen, "Encircle the dark to seal every foe before time runs out.", ScreenHeight/2+4)
		drawCenter(screen, "[Click or Space to start]", ScreenHeight/2+34)
	case StateCleared:
		next := g.currentStage + 2 // 1-indexed display of the next stage
		if g.currentStage+1 >= len(stages) {
			drawCenter(screen, "S T A G E   C L E A R", ScreenHeight/2-10)
			drawCenter(screen, "Final stage cleared!", ScreenHeight/2+14)
		} else {
			drawCenter(screen, "S T A G E   C L E A R", ScreenHeight/2-10)
			drawCenter(screen, fmt.Sprintf("Stage %d coming up...", next), ScreenHeight/2+14)
		}
	case StateGameOver:
		drawCenter(screen, "T I M E   U P", ScreenHeight/2-10)
		drawCenter(screen, "[Click to retry this stage]", ScreenHeight/2+14)
	case StateAllCleared:
		drawCenter(screen, "A L L   C L E A R", ScreenHeight/2-20)
		drawCenter(screen, "You painted the night.", ScreenHeight/2+4)
		drawCenter(screen, "[Click to return to title]", ScreenHeight/2+28)
	}
}

func drawCenter(screen *ebiten.Image, msg string, y int) {
	w := len(msg) * 6
	ebitenutil.DebugPrintAt(screen, msg, ScreenWidth/2-w/2, y)
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
	ebiten.SetWindowSize(ScreenWidth, ScreenHeight)
	ebiten.SetWindowTitle("Rift")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(newGame()); err != nil {
		log.Fatal(err)
	}
}
