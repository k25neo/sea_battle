// Package game содержит чистую логику морского боя: поле, корабли, выстрелы.
package game

import (
	"errors"
	"math/rand"
)

// Size — сторона поля (Size×Size клеток).
const Size = 10

// FleetSizes — классический флот: 1×4, 2×3, 3×2, 4×1.
var FleetSizes = []int{4, 3, 3, 2, 2, 2, 1, 1, 1, 1}

// Cell — клетка поля.
type Cell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Ship — корабль из последовательности клеток.
type Ship struct {
	Cells []Cell
	hits  map[Cell]bool
}

// Sunk возвращает true, если все клетки корабля подбиты.
func (s *Ship) Sunk() bool { return len(s.hits) == len(s.Cells) }

// ShotResult — результат выстрела.
type ShotResult struct {
	Hit   bool   `json:"hit"`
	Sunk  bool   `json:"sunk"`
	Cells []Cell `json:"cells"` // клетки потопленного корабля либо клетка выстрела
	Area  []Cell `json:"area"`  // «ореол» вокруг потопленного корабля
}

// Board — игровое поле одного игрока.
type Board struct {
	ships    []*Ship
	occupied map[Cell]*Ship // клетка -> корабль
	shots    map[Cell]bool  // куда уже стреляли
}

// NewBoard создаёт пустое поле.
func NewBoard() *Board {
	return &Board{
		occupied: make(map[Cell]*Ship),
		shots:    make(map[Cell]bool),
	}
}

func inBounds(c Cell) bool {
	return c.X >= 0 && c.X < Size && c.Y >= 0 && c.Y < Size
}

// Place ставит корабль на поле. Проверяет форму, прямизну, отсутствие
// пересечений и соседства (в том числе по диагонали) с другими кораблями.
func (b *Board) Place(cells []Cell) error {
	if len(cells) == 0 || len(cells) > 4 {
		return errors.New("некорректная длина корабля")
	}
	seen := make(map[Cell]bool, len(cells))
	for _, c := range cells {
		if !inBounds(c) {
			return errors.New("корабль вне поля")
		}
		if seen[c] {
			return errors.New("повторяющаяся клетка")
		}
		seen[c] = true
	}
	// Корабль должен быть прямой линией без разрывов.
	horizontal := allSameY(cells)
	vertical := allSameX(cells)
	if !horizontal && !vertical {
		return errors.New("корабль должен быть прямой линией")
	}
	sortCells(cells, horizontal)
	for i := 1; i < len(cells); i++ {
		if horizontal && cells[i].X != cells[i-1].X+1 ||
			vertical && cells[i].Y != cells[i-1].Y+1 {
			return errors.New("в корабле есть разрыв")
		}
	}
	// Проверка соседства (включая диагонали) и пересечений.
	for _, c := range cells {
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				n := Cell{c.X + dx, c.Y + dy}
				if _, ok := b.occupied[n]; ok {
					return errors.New("корабли стоят слишком близко")
				}
			}
		}
	}
	ship := &Ship{Cells: cells, hits: make(map[Cell]bool)}
	for _, c := range cells {
		b.occupied[c] = ship
	}
	b.ships = append(b.ships, ship)
	return nil
}

// PlaceFleet расставляет весь флот: сначала очищает поле, затем ставит
// корабли и проверяет, что состав совпадает с классическим.
func (b *Board) PlaceFleet(fleet [][]Cell) error {
	clean := NewBoard()
	sizes := make([]int, 0, len(fleet))
	for _, s := range fleet {
		if err := clean.Place(s); err != nil {
			return err
		}
		sizes = append(sizes, len(s))
	}
	if !sameSizes(sizes, FleetSizes) {
		return errors.New("некорректный состав флота")
	}
	*b = *clean
	return nil
}

// Randomize заполняет поле случаем размещённым флотом.
func (b *Board) Randomize() {
	for attempts := 0; attempts < 100; attempts++ {
		clean := NewBoard()
		ok := true
		for _, size := range FleetSizes {
			if !randomShip(clean, size) {
				ok = false
				break
			}
		}
		if ok {
			*b = *clean
			return
		}
	}
}

func randomShip(b *Board, size int) bool {
	for i := 0; i < 200; i++ {
		horizontal := rand.Intn(2) == 0
		var maxPos Cell
		if horizontal {
			maxPos = Cell{Size - size, Size - 1}
		} else {
			maxPos = Cell{Size - 1, Size - size}
		}
		start := Cell{rand.Intn(maxPos.X + 1), rand.Intn(maxPos.Y + 1)}
		cells := make([]Cell, size)
		for j := range cells {
			cells[j] = start
			if horizontal {
				cells[j].X += j
			} else {
				cells[j].Y += j
			}
		}
		if b.Place(cells) == nil {
			return true
		}
	}
	return false
}

// Shoot обрабатывает выстрел в клетку (x, y).
func (b *Board) Shoot(x, y int) (ShotResult, error) {
	c := Cell{X: x, Y: y}
	if !inBounds(c) {
		return ShotResult{}, errors.New("выстрел вне поля")
	}
	if b.shots[c] {
		return ShotResult{}, errors.New("сюда уже стреляли")
	}
	b.shots[c] = true
	ship, hit := b.occupied[c]
	if !hit {
		return ShotResult{Cells: []Cell{c}}, nil
	}
	ship.hits[c] = true
	if !ship.Sunk() {
		return ShotResult{Hit: true, Cells: []Cell{c}}, nil
	}
	return ShotResult{Hit: true, Sunk: true, Cells: ship.Cells, Area: halo(ship.Cells)}, nil
}

// halo возвращает клетки «ореола» вокруг корабля (в пределах поля).
func halo(cells []Cell) []Cell {
	around := make(map[Cell]bool)
	ship := make(map[Cell]bool, len(cells))
	for _, c := range cells {
		ship[c] = true
	}
	for _, c := range cells {
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				n := Cell{c.X + dx, c.Y + dy}
				if inBounds(n) && !ship[n] {
					around[n] = true
				}
			}
		}
	}
	out := make([]Cell, 0, len(around))
	for c := range around {
		out = append(out, c)
	}
	return out
}

// Mark — отметка о выстреле для восстановления картины поля.
type Mark struct {
	X    int  `json:"x"`
	Y    int  `json:"y"`
	Hit  bool `json:"hit"`
	Sunk bool `json:"sunk"`
}

// Report — снимок поля: расположение кораблей, все выстрелы и ореолы
// вокруг потопленных. Используется для восстановления состояния при
// переподключении игрока.
func (b *Board) Report() (ships [][]Cell, marks []Mark, area []Cell) {
	areaSet := make(map[Cell]bool)
	for _, s := range b.ships {
		ships = append(ships, s.Cells)
		if s.Sunk() {
			for _, c := range halo(s.Cells) {
				areaSet[c] = true
			}
		}
	}
	for c := range b.shots {
		m := Mark{X: c.X, Y: c.Y}
		if s, ok := b.occupied[c]; ok {
			m.Hit = true
			m.Sunk = s.Sunk()
		}
		marks = append(marks, m)
	}
	for c := range areaSet {
		area = append(area, c)
	}
	return ships, marks, area
}

// AllSunk возвращает true, если весь флот потоплен.
func (b *Board) AllSunk() bool {
	if len(b.ships) == 0 {
		return false
	}
	for _, s := range b.ships {
		if !s.Sunk() {
			return false
		}
	}
	return true
}

func allSameX(cells []Cell) bool {
	for _, c := range cells[1:] {
		if c.X != cells[0].X {
			return false
		}
	}
	return true
}

func allSameY(cells []Cell) bool {
	for _, c := range cells[1:] {
		if c.Y != cells[0].Y {
			return false
		}
	}
	return true
}

func sortCells(cells []Cell, horizontal bool) {
	for i := 1; i < len(cells); i++ {
		for j := i; j > 0; j++ {
			a, b := cells[j-1], cells[j]
			if (horizontal && a.X > b.X) || (!horizontal && a.Y > b.Y) {
				cells[j-1], cells[j] = b, a
			} else {
				break
			}
		}
	}
}

func sameSizes(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[int]int, len(b))
	for _, n := range b {
		count[n]++
	}
	for _, n := range a {
		count[n]--
		if count[n] < 0 {
			return false
		}
	}
	return true
}
