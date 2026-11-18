package game

import "testing"

func cells(xs ...int) []Cell {
	// xs: пары x,y
	out := make([]Cell, 0, len(xs)/2)
	for i := 0; i < len(xs); i += 2 {
		out = append(out, Cell{xs[i], xs[i+1]})
	}
	return out
}

func TestPlaceValid(t *testing.T) {
	b := NewBoard()
	if err := b.Place(cells(0, 0, 1, 0, 2, 0)); err != nil {
		t.Fatalf("горизонтальный корабль: %v", err)
	}
	if err := b.Place(cells(5, 5)); err != nil {
		t.Fatalf("одиночный корабль: %v", err)
	}
	if err := b.Place(cells(8, 2, 8, 3)); err != nil {
		t.Fatalf("вертикальный корабль: %v", err)
	}
}

func TestPlaceInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input []Cell
	}{
		{"вне поля", cells(9, 0, 10, 0)},
		{"не линия", cells(0, 0, 1, 1)},
		{"разрыв", cells(0, 0, 2, 0)},
		{"слишком длинный", cells(0, 0, 1, 0, 2, 0, 3, 0, 4, 0)},
	}
	for _, tc := range tests {
		b := NewBoard()
		if err := b.Place(tc.input); err == nil {
			t.Errorf("%s: ожидалась ошибка", tc.name)
		}
	}
}

func TestPlaceAdjacentForbidden(t *testing.T) {
	b := NewBoard()
	if err := b.Place(cells(2, 2, 3, 2)); err != nil {
		t.Fatal(err)
	}
	// вплотную сбоку
	if err := b.Place(cells(4, 2)); err == nil {
		t.Error("боковое соседство должно запрещаться")
	}
	// по диагонали
	if err := b.Place(cells(4, 3)); err == nil {
		t.Error("диагональное соседство должно запрещаться")
	}
	// через одну — можно
	if err := b.Place(cells(5, 4)); err != nil {
		t.Errorf("через клетку можно ставить: %v", err)
	}
}

func TestShootHitSunk(t *testing.T) {
	b := NewBoard()
	if err := b.Place(cells(0, 0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	res, err := b.Shoot(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Hit || res.Sunk {
		t.Fatalf("первое попадание: %+v", res)
	}
	res, err = b.Shoot(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Hit || !res.Sunk {
		t.Fatalf("второе попадание должно топить: %+v", res)
	}
	if len(res.Cells) != 2 {
		t.Errorf("ожидалось 2 клетки корабля, получено %d", len(res.Cells))
	}
	// ореол вокруг двухпалубника у угла: (0,1) (1,1) (2,0) (2,1)
	if len(res.Area) != 4 {
		t.Errorf("ожидался ореол из 4 клеток, получено %d: %v", len(res.Area), res.Area)
	}
}

func TestShootMissAndRepeat(t *testing.T) {
	b := NewBoard()
	res, err := b.Shoot(5, 5)
	if err != nil || res.Hit {
		t.Fatalf("промах: %+v, %v", res, err)
	}
	if _, err := b.Shoot(5, 5); err == nil {
		t.Error("повторный выстрел должен отклоняться")
	}
	if _, err := b.Shoot(10, 0); err == nil {
		t.Error("выстрел вне поля должен отклоняться")
	}
}

func TestAllSunk(t *testing.T) {
	b := NewBoard()
	if b.AllSunk() {
		t.Error("пустое поле не считается потопленным")
	}
	if err := b.Place(cells(0, 0)); err != nil {
		t.Fatal(err)
	}
	if b.AllSunk() {
		t.Error("живой корабль")
	}
	if _, err := b.Shoot(0, 0); err != nil {
		t.Fatal(err)
	}
	if !b.AllSunk() {
		t.Error("флот из одного корабля должен быть потоплен")
	}
}

func TestPlaceFleetFull(t *testing.T) {
	b := NewBoard()
	b.Randomize()
	// после Randomize поле заполнено; повторная полная расстановка тоже должна работать
	fleet := [][]Cell{
		cells(0, 0, 1, 0, 2, 0, 3, 0),
		cells(0, 2, 1, 2, 2, 2),
		cells(0, 4, 1, 4, 2, 4),
		cells(5, 0, 6, 0),
		cells(5, 2, 6, 2),
		cells(5, 4, 6, 4),
		cells(0, 6),
		cells(0, 8),
		cells(2, 8),
		cells(4, 8),
	}
	if err := b.PlaceFleet(fleet); err != nil {
		t.Fatalf("полная расстановка: %v", err)
	}
	if wrong := len(b.ships); wrong != 10 {
		t.Errorf("ожидалось 10 кораблей, получено %d", wrong)
	}
}

func TestPlaceFleetWrongComposition(t *testing.T) {
	b := NewBoard()
	fleet := make([][]Cell, 10)
	for i := range fleet {
		fleet[i] = cells(i%5, (i/5)*3+1) // 10 однопалубных
	}
	if err := b.PlaceFleet(fleet); err == nil {
		t.Error("некорректный состав флота должен отклоняться")
	}
}

func TestRandomizeAlwaysValid(t *testing.T) {
	for i := 0; i < 50; i++ {
		b := NewBoard()
		b.Randomize()
		if len(b.ships) != 10 {
			t.Fatalf("расставлено %d кораблей", len(b.ships))
		}
		for _, s := range b.ships {
			for _, c := range s.Cells {
				for dx := -1; dx <= 1; dx++ {
					for dy := -1; dy <= 1; dy++ {
						n := Cell{c.X + dx, c.Y + dy}
						if other, ok := b.occupied[n]; ok && other != s {
							t.Fatalf("корабли соприкасаются: %v и %v", s.Cells, other.Cells)
						}
					}
				}
			}
		}
	}
}
