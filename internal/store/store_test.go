package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnsureProfileAndMerge(t *testing.T) {
	s := newTestStore(t)

	// Гость без ВК.
	pid, err := s.EnsureProfile(0, "abc-123", "Гость")
	if err != nil || pid != "g:abc-123" {
		t.Fatalf("гостевой профиль: %q %v", pid, err)
	}
	// Мусорный guestID — аноним, без записи.
	if pid, _ := s.EnsureProfile(0, "мусор/со\\slashes", "Х"); pid != "" {
		t.Errorf("мусорный guestID должен давать пустой профиль, получено %q", pid)
	}

	// Одна победа под гостевым id.
	s.EnsureProfile(777, "", "ВК-парень")
	if w, _, _, _, err := s.RecordGame("g:abc-123", "vk:777"); err != nil || w == 0 {
		t.Fatalf("партия не записана: %v", err)
	}

	// Тот же человек зашёл под VK ID — партии переехали на vk-профиль.
	pid, err = s.EnsureProfile(42, "abc-123", "Владимир")
	if err != nil || pid != "vk:42" {
		t.Fatalf("vk-профиль: %q %v", pid, err)
	}
	_, me, _, err := s.Leaders(10, "vk:42", 0)
	if err != nil {
		t.Fatal(err)
	}
	if me == nil || me.Name != "Владимир" || me.Games != 1 || me.Wins != 1 {
		t.Fatalf("перенос гостевого прогресса не получился: %+v", me)
	}
	// Старый гостевой профиль не должен светиться в топе отдельной строкой.
	leaders, _, _, _ := s.Leaders(10, "", 0)
	for _, p := range leaders {
		if p.ID == "g:abc-123" {
			t.Error("гостевой профиль остался отдельной записью после привязки")
		}
	}
}

func TestEloMovesSensibly(t *testing.T) {
	s := newTestStore(t)
	s.EnsureProfile(1, "", "A")
	s.EnsureProfile(2, "", "B")

	// Равные на старте: победа +20 (K=40 первые 10 партий).
	wElo, wD, lElo, lD, err := s.RecordGame("vk:1", "vk:2")
	if err != nil {
		t.Fatal(err)
	}
	if wD != 20 || lD != -20 || wElo != 1520 || lElo != 1480 {
		t.Fatalf("равные: ждали ±20 и 1520/1480, получили %+d/%+d %d/%d", wD, lD, wElo, lElo)
	}

	// Аутсайдер берёт реванш: дельта круче, чем у фаворита.
	_, wD2, _, lD2, err := s.RecordGame("vk:2", "vk:1")
	if err != nil {
		t.Fatal(err)
	}
	if wD2 <= wD || lD2 >= lD {
		t.Errorf("апсет должен весеть больше: %+d/%+d против %+d/%+d", wD2, lD2, wD, lD)
	}
	if abs(wD2+lD2) > 1 {
		t.Errorf("ЭЛО почти сумма нулевая, получили %+d и %+d", wD2, lD2)
	}

	// Анонимы (пустые id) — мимо рейтинга, без паники.
	if w, _, _, _, err := s.RecordGame("", "vk:1"); err != nil || w != 0 {
		t.Errorf("аноним-победитель должен игнорироваться: %v %d", err, w)
	}
}

func TestLeadersOrderAndThreshold(t *testing.T) {
	s := newTestStore(t)
	names := map[int]string{1: "Чемп", 2: "Середняк", 3: "Однопартиец", 9: "Новичок"}
	for _, id := range []int{1, 2, 3, 9} {
		if _, err := s.EnsureProfile(int64(id), "", names[id]); err != nil {
			t.Fatal(err)
		}
	}
	// 3 победы Чемпа + 1 реванш Середняка: у обоих ≥3 партий, Чемп впереди.
	s.RecordGame("vk:1", "vk:2")
	s.RecordGame("vk:1", "vk:2")
	s.RecordGame("vk:1", "vk:2")
	s.RecordGame("vk:2", "vk:1")
	// Одна сенсационная победа новичка с нулевым послужным — не в топе.
	s.RecordGame("vk:3", "vk:1")

	leaders, _, _, err := s.Leaders(10, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaders) != 2 {
		t.Fatalf("в топе должно быть двое, получили %+v", leaders)
	}
	if leaders[0].Name != "Чемп" || leaders[1].Name != "Середняк" {
		t.Errorf("порядок в топе неверный: %+v", leaders)
	}

	// «Я» возвращается даже вне топа.
	_, me, _, err := s.Leaders(1, "vk:9", 3)
	if err != nil {
		t.Fatal(err)
	}
	if me == nil || me.Name != "Новичок" {
		t.Errorf("me не вернулся: %+v", me)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
