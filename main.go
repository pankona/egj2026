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

	MaxCharge     = 900.0
	ChargeRecover = 3.6
	BladeRadius   = 2 // cells

	LightThresholdCount = 0.35 // cells with light above this counted as bright
	WallLightThreshold  = 0.5  // cells brighter than this block flood fill

	EnemyErodeRadius = 18.0  // px, footprint of darkening
	EnemyErodeRate   = 0.020 // per frame, interior cells
	EnemyEdgeBoost   = 3.5   // multiplier for cells on the light boundary
	EnemyHomingPx    = 260.0 // search radius for nearest light edge
	EnemyRetargetSec = 0.75  // re-pick a target this often
)

// Stage describes one playable level. The 10-stage progression in the GDD
// builds enemy count, speed, and the light-percentage target side by side.
// Stage 10 (the giant boss) will plug into this same shape later.
type Stage struct {
	Enemies      int
	EnemySpeed   float64
	WinThreshold float64
	TimeLimit    float64
	Boss         bool // ignore Enemies/WinThreshold; spawn one giant and clear on seal
}

var stages = []Stage{
	{Enemies: 0, EnemySpeed: 0.00, WinThreshold: 0.20, TimeLimit: 30}, // 1 tutorial
	{Enemies: 1, EnemySpeed: 0.40, WinThreshold: 0.30, TimeLimit: 45}, // 2
	{Enemies: 2, EnemySpeed: 0.40, WinThreshold: 0.30, TimeLimit: 45}, // 3
	{Enemies: 2, EnemySpeed: 0.55, WinThreshold: 0.40, TimeLimit: 55}, // 4
	{Enemies: 3, EnemySpeed: 0.55, WinThreshold: 0.40, TimeLimit: 55}, // 5
	{Enemies: 3, EnemySpeed: 0.70, WinThreshold: 0.50, TimeLimit: 60}, // 6
	{Enemies: 4, EnemySpeed: 0.70, WinThreshold: 0.50, TimeLimit: 60}, // 7
	{Enemies: 3, EnemySpeed: 0.90, WinThreshold: 0.60, TimeLimit: 65}, // 8
	{Enemies: 4, EnemySpeed: 0.90, WinThreshold: 0.60, TimeLimit: 65}, // 9
	{Enemies: 0, EnemySpeed: 0.35, WinThreshold: 0.70, TimeLimit: 90, Boss: true}, // 10 boss
}

// DebugMode is initialized in debug_mode_default.go / debug_mode_wasm.go.
var DebugMode bool

type Cell struct {
	Light   float32
	R, G, B float32
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

	currentStage      int
	dragging          bool
	prevMX, prevMY    int
	charge            float64
	hue               float64
	stageTime         float64
	lightPercent      float64
	rng               *rand.Rand
	postClearCooldown int
}

func newGame() *Game {
	g := &Game{
		state:  StateTitle,
		charge: MaxCharge,
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
	g.charge = MaxCharge
	g.stageTime = s.TimeLimit
	g.dragging = false
	g.lightPercent = 0
	g.hue = g.rng.Float64() * 360

	g.enemies = g.enemies[:0]
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
	for i := 0; i < s.Enemies; i++ {
		g.enemies = append(g.enemies, &Enemy{
			X:            g.rng.Float64()*float64(ScreenWidth-160) + 80,
			Y:            g.rng.Float64()*float64(ScreenHeight-160) + 80,
			VX:           (g.rng.Float64()*2 - 1) * s.EnemySpeed,
			VY:           (g.rng.Float64()*2 - 1) * s.EnemySpeed,
			Speed:        s.EnemySpeed,
			Radius:       12,
			EffectRadius: EnemyErodeRadius,
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

	wasDragging := g.dragging
	if pressed && g.charge > 0 {
		if !g.dragging {
			g.dragging = true
			g.prevMX, g.prevMY = mx, my
			g.hue += 47 + g.rng.Float64()*30
		}
		g.cutLine(g.prevMX, g.prevMY, mx, my)
		g.prevMX, g.prevMY = mx, my
	} else {
		g.dragging = false
	}
	if wasDragging && !g.dragging {
		g.claimEnclosure()
	}

	if !pressed {
		g.charge = math.Min(MaxCharge, g.charge+ChargeRecover)
	}

	for _, e := range g.enemies {
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

	s := stages[g.currentStage]
	cleared := false
	if s.Boss {
		cleared = len(g.enemies) == 0
	} else {
		cleared = g.lightPercent >= s.WinThreshold
	}
	if cleared {
		g.state = StateCleared
		g.postClearCooldown = 60
	}
}

func (g *Game) cutLine(x0, y0, x1, y1 int) {
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	dist := math.Sqrt(dx*dx + dy*dy)

	cost := dist
	if cost > g.charge {
		cost = g.charge
	}
	g.charge -= cost

	steps := int(dist) + 1
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := int(float64(x0) + t*dx)
		py := int(float64(y0) + t*dy)
		g.illuminate(px, py)
	}
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

// claimEnclosure detects any dark region fully enclosed by lit cells and the
// screen border, then fills it with light in the current hue. Enemies caught
// inside are sealed (removed). Run once on drag release.
func (g *Game) claimEnclosure() {
	const wall = WallLightThreshold

	var outside [GridWidth][GridHeight]bool
	queue := make([][2]int, 0, 512)

	enqueueIfDark := func(x, y int) {
		if outside[x][y] {
			return
		}
		if g.grid[x][y].Light >= wall {
			return
		}
		outside[x][y] = true
		queue = append(queue, [2]int{x, y})
	}
	for x := 0; x < GridWidth; x++ {
		enqueueIfDark(x, 0)
		enqueueIfDark(x, GridHeight-1)
	}
	for y := 0; y < GridHeight; y++ {
		enqueueIfDark(0, y)
		enqueueIfDark(GridWidth-1, y)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := p[0]+d[0], p[1]+d[1]
			if nx < 0 || nx >= GridWidth || ny < 0 || ny >= GridHeight {
				continue
			}
			if outside[nx][ny] {
				continue
			}
			if g.grid[nx][ny].Light >= wall {
				continue
			}
			outside[nx][ny] = true
			queue = append(queue, [2]int{nx, ny})
		}
	}

	r, gC, b := hueToRGB(g.hue)
	var inside [GridWidth][GridHeight]bool
	claimed := 0
	for x := 0; x < GridWidth; x++ {
		for y := 0; y < GridHeight; y++ {
			if outside[x][y] || g.grid[x][y].Light >= wall {
				continue
			}
			inside[x][y] = true
			claimed++
			c := &g.grid[x][y]
			c.Light = 1
			c.R, c.G, c.B = r, gC, b
		}
	}
	if claimed == 0 {
		return
	}

	survivors := g.enemies[:0]
	for _, e := range g.enemies {
		cx := int(e.X) / CellSize
		cy := int(e.Y) / CellSize
		if cx >= 0 && cx < GridWidth && cy >= 0 && cy < GridHeight && inside[cx][cy] {
			continue
		}
		survivors = append(survivors, e)
	}
	g.enemies = survivors
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

	for _, e := range g.enemies {
		cx := int(e.X) / CellSize
		cy := int(e.Y) / CellSize
		light := float32(0)
		if cx >= 0 && cx < GridWidth && cy >= 0 && cy < GridHeight {
			light = g.grid[cx][cy].Light
		}
		alpha := uint8(40 + clamp01(light)*210)
		r := float32(e.Radius)
		vector.DrawFilledCircle(screen, float32(e.X), float32(e.Y), r,
			color.RGBA{18, 14, 22, alpha}, true)
		strokeW := float32(1.5)
		if e.IsBoss {
			strokeW = 2.5
		}
		vector.StrokeCircle(screen, float32(e.X), float32(e.Y), r, strokeW,
			color.RGBA{0, 0, 0, alpha}, true)
	}

	chargeW := float32(220.0)
	ratio := float32(g.charge / MaxCharge)
	vector.DrawFilledRect(screen, 10, 10, chargeW, 8, color.RGBA{40, 40, 50, 220}, false)
	vector.DrawFilledRect(screen, 10, 10, chargeW*ratio, 8, color.RGBA{200, 230, 255, 240}, false)

	s := stages[g.currentStage]
	msg := fmt.Sprintf("Stage %d/%d   Light %3.0f%% / %2.0f%%   Time %4.1fs",
		g.currentStage+1, len(stages), g.lightPercent*100, s.WinThreshold*100, g.stageTime)
	ebitenutil.DebugPrintAt(screen, msg, 10, 24)

	if DebugMode {
		ebitenutil.DebugPrintAt(screen,
			fmt.Sprintf("FPS %4.1f", ebiten.ActualFPS()), 10, ScreenHeight-18)
	}

	switch g.state {
	case StateTitle:
		drawCenter(screen, "R I F T", ScreenHeight/2-40)
		drawCenter(screen, "Drag to cut the dark. Encircle to claim.", ScreenHeight/2-10)
		drawCenter(screen, "Reach the light threshold before time runs out.", ScreenHeight/2+4)
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
