'use strict';

const SIZE = 10;
const SIZES = [4, 3, 3, 2, 2, 2, 1, 1, 1, 1];
const LETTERS = 'АБВГДЕЖЗИК';

let ws = null;
let side = -1;
let oppName = '';
let myTurn = false;
let phase = 'menu'; // menu | placement | battle

// --- Расстановка ---
let ships = [];        // [{sizeIdx, cells}]
let horizontal = true;
let fleetSent = false;

// --- Бой ---
let myMarks = {};      // 'x,y' -> miss|hit|sunk|area — выстрелы по мне
let enemyMarks = {};   // 'x,y' -> miss|hit|sunk|area — мои выстрелы

const $ = (id) => document.getElementById(id);
const key = (x, y) => x + ',' + y;

// ---------- Построение полей ----------

function buildBoard(container, clickable) {
  container.innerHTML = '';
  for (let y = 0; y <= SIZE; y++) {
    for (let x = 0; x <= SIZE; x++) {
      const d = document.createElement('div');
      if (x === 0 && y === 0) {
        d.className = 'corner';
      } else if (y === 0) {
        d.className = 'label';
        d.textContent = LETTERS[x - 1];
      } else if (x === 0) {
        d.className = 'label';
        d.textContent = y;
      } else {
        d.className = 'cell';
        d.dataset.x = x - 1;
        d.dataset.y = y - 1;
      }
      container.appendChild(d);
    }
  }
  if (clickable) {
    container.onclick = (e) => {
      const c = e.target.closest('.cell');
      if (c) onEnemyClick(+c.dataset.x, +c.dataset.y);
    };
  }
}

function cellOf(container, x, y) {
  return container.querySelector(`.cell[data-x="${x}"][data-y="${y}"]`);
}

// ---------- Расстановка ----------

function nextSizeIdx() {
  const used = SIZES.map(() => 0);
  for (const s of ships) used[s.sizeIdx]++;
  return used.findIndex((n, i) => n < 1);
}

function shipCells(x, y, size) {
  const cells = [];
  for (let i = 0; i < size; i++) {
    const c = { x: horizontal ? x + i : x, y: horizontal ? y : y + i };
    if (c.x >= SIZE || c.y >= SIZE) return null;
    cells.push(c);
  }
  return cells;
}

function isNear(cx, cy, x, y) {
  return Math.abs(cx - x) <= 1 && Math.abs(cy - y) <= 1;
}

function canPlace(cells, excludeShip) {
  for (const s of ships) {
    if (s === excludeShip) continue;
    for (const sc of s.cells) {
      for (const c of cells) {
        if (isNear(sc.x, sc.y, c.x, c.y)) return false;
      }
    }
  }
  return true;
}

function renderPlaceBoard() {
  const b = $('place-board');
  b.querySelectorAll('.cell').forEach((c) => (c.className = 'cell'));
  const idx = nextSizeIdx();
  for (const s of ships) {
    for (const c of s.cells) {
      cellOf(b, c.x, c.y).classList.add('ship');
    }
  }
  $('fleet-left').innerHTML = SIZES
    .map((n, i) => `<div class="fship ${i === idx ? 'next' : ''} ${i < idx ? 'done' : ''}">` +
      '<i></i>'.repeat(n) + '</div>')
    .join('');
  $('btn-ready').disabled = !(ships.length === SIZES.length) || fleetSent;
}

function preview(cells, ok) {
  const b = $('place-board');
  clearPreview();
  if (!cells) return;
  previewCells = cells;
  for (const c of cells) {
    const el = cellOf(b, c.x, c.y);
    if (el) el.classList.add(ok ? 'preview' : 'preview-bad');
  }
}

let previewCells = [];
function clearPreview() {
  const b = $('place-board');
  for (const c of previewCells) {
    const el = cellOf(b, c.x, c.y);
    if (el) el.classList.remove('preview', 'preview-bad');
  }
  previewCells = [];
}

function setupPlacement() {
  const b = $('place-board');
  buildBoard(b, false);
  b.addEventListener('click', (e) => {
    const c = e.target.closest('.cell');
    if (!c || fleetSent) return;
    const x = +c.dataset.x, y = +c.dataset.y;
    // Клик по уже поставленному кораблю — убрать его.
    const existing = ships.find((s) => s.cells.some((sc) => sc.x === x && sc.y === y));
    if (existing) {
      ships = ships.filter((s) => s !== existing);
      renderPlaceBoard();
      return;
    }
    const idx = nextSizeIdx();
    if (idx === -1) return;
    const cells = shipCells(x, y, SIZES[idx]);
    if (!cells || !canPlace(cells)) {
      flashToast('Здесь нельзя поставить корабль');
      return;
    }
    ships.push({ sizeIdx: idx, cells });
    clearPreview();
    renderPlaceBoard();
  });
  b.addEventListener('mousemove', (e) => {
    const c = e.target.closest('.cell');
    if (!c || fleetSent) { clearPreview(); return; }
    const x = +c.dataset.x, y = +c.dataset.y;
    const idx = nextSizeIdx();
    if (idx === -1) { clearPreview(); return; }
    const cells = shipCells(x, y, SIZES[idx]);
    if (!cells || !canPlace(cells)) {
      preview(cells, false);
    } else {
      preview(cells, true);
    }
  });
  b.addEventListener('mouseleave', clearPreview);
}

function randomFleet() {
  ships = [];
  for (let idx = 0; idx < SIZES.length; idx++) {
    for (let tries = 0; tries < 300; tries++) {
      horizontal = Math.random() < 0.5;
      const x = Math.floor(Math.random() * (horizontal ? SIZE - SIZES[idx] + 1 : SIZE));
      const y = Math.floor(Math.random() * (horizontal ? SIZE : SIZE - SIZES[idx] + 1));
      const cells = shipCells(x, y, SIZES[idx]);
      if (cells && canPlace(cells)) {
        ships.push({ sizeIdx: idx, cells });
        break;
      }
    }
  }
  renderPlaceBoard();
}

// ---------- Бой ----------

function onEnemyClick(x, y) {
  if (phase !== 'battle' || !myTurn) return;
  if (enemyMarks[key(x, y)]) return;
  ws.send(JSON.stringify({ type: 'fire', x, y }));
  myTurn = false; // до ответа сервера клики запрещены
}

function applyMarks(container, marks) {
  for (const [k, v] of Object.entries(marks)) {
    const [x, y] = k.split(',').map(Number);
    const el = cellOf(container, x, y);
    if (el) el.classList.add(v);
  }
}

function renderBoards() {
  const my = $('my-board'), en = $('enemy-board');
  my.querySelectorAll('.cell').forEach((c) => (c.className = 'cell'));
  en.querySelectorAll('.cell').forEach((c) => (c.className = 'cell'));
  for (const s of ships) {
    for (const c of s.cells) cellOf(my, c.x, c.y).classList.add('ship');
  }
  applyMarks(my, myMarks);
  applyMarks(en, enemyMarks);
}

function markShot(marks, msg) {
  if (msg.sunk) {
    for (const c of msg.cells) marks[key(c.x, c.y)] = 'sunk';
    for (const c of msg.area) marks[key(c.x, c.y)] = 'area';
  } else {
    marks[key(msg.x, msg.y)] = msg.hit ? 'hit' : 'miss';
  }
}

function setStatus(text) {
  $('battle-status').textContent = text;
}

// ---------- Сообщения сервера ----------

function handleMessage(msg) {
  switch (msg.type) {
    case 'room':
      side = msg.side;
      $('room-code').textContent = msg.code;
      phase = 'placement';
      fleetSent = false;
      ships = [];
      myMarks = {};
      enemyMarks = {};
      oppName = '';
      $('placement-status').textContent = 'Ожидание второго игрока…';
      showScreen('placement');
      renderPlaceBoard();
      break;
    case 'opponent':
      oppName = msg.name;
      $('placement-status').textContent = `${msg.name} в сети — расставьте корабли`;
      $('opp-name-own').textContent = `(${msg.name})`;
      $('opp-name-enemy').textContent = `(${msg.name})`;
      break;
    case 'placed':
      $('placement-status').textContent = 'Флот готов, ждём соперника…';
      break;
    case 'start':
      if (phase !== 'battle') {
        phase = 'battle';
        myMarks = {};
        enemyMarks = {};
        buildBoard($('my-board'), false);
        buildBoard($('enemy-board'), true);
        showScreen('battle');
      }
      myTurn = msg.youTurn;
      setStatus(myTurn ? 'Ваш ход — стреляйте по полю противника' : `Ход ${oppName || 'соперника'}…`);
      renderBoards();
      break;
    case 'shot': {
      const myShot = msg.target === 'enemy';
      markShot(myShot ? enemyMarks : myMarks, msg);
      renderBoards();
      if (myShot) {
        setStatus(msg.hit ? 'Попадание! Стреляйте ещё' : 'Мимо…');
      }
      break;
    }
    case 'game_over':
      phase = 'menu';
      showOverlay(msg.youWon ? '🏆 Победа!' : '💥 Поражение',
        msg.youWon ? 'Весь флот противника потоплен.' : 'Ваш флот уничтожен.');
      break;
    case 'opponent_left':
      phase = 'menu';
      showOverlay('Соперник отключился', 'Игра прервана.');
      break;
    case 'error':
      flashToast(msg.message);
      if (msg.message === 'сейчас не ваш ход') {
        myTurn = false;
      }
      break;
  }
}

// ---------- Сеть ----------

function connect(sendObj) {
  if (ws) {
    try { ws.close(); } catch (e) {}
  }
  // URL эндпоинта берём относительно адреса страницы,
  // чтобы работало и в корне, и под префиксом /sea/.
  const u = new URL('ws', location.href);
  u.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(u.href);
  ws.onopen = () => ws.send(JSON.stringify(sendObj));
  ws.onmessage = (e) => {
    try { handleMessage(JSON.parse(e.data)); } catch (err) {}
  };
  ws.onclose = () => {
    if (phase === 'battle' || phase === 'placement') {
      phase = 'menu';
      showOverlay('Соединение потеряно', 'Проверьте сеть и попробуйте снова.');
    }
  };
}

// ---------- Экраны и прочее ----------

function showScreen(name) {
  for (const s of ['menu', 'placement', 'battle']) {
    $('screen-' + s).classList.toggle('hidden', s !== name);
  }
}

function showOverlay(title, text) {
  $('overlay-title').textContent = title;
  $('overlay-text').textContent = text;
  $('overlay').classList.remove('hidden');
}

let toastTimer = null;
function flashToast(text) {
  const t = $('toast');
  t.textContent = text;
  t.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add('hidden'), 2500);
}

// ---------- Кнопки ----------

$('btn-create').onclick = () => {
  const name = $('name-input').value.trim() || 'Игрок';
  connect({ type: 'create', name });
};

$('btn-join').onclick = () => {
  const name = $('name-input').value.trim() || 'Игрок';
  const code = $('join-code').value.trim().toUpperCase();
  if (!code) { flashToast('Введите код комнаты'); return; }
  connect({ type: 'join', code, name });
};

$('btn-rotate').onclick = () => {
  horizontal = !horizontal;
  renderPlaceBoard();
};

$('btn-random').onclick = () => {
  if (!fleetSent) randomFleet();
};

$('btn-clear').onclick = () => {
  if (!fleetSent) {
    ships = [];
    renderPlaceBoard();
  }
};

$('btn-ready').onclick = () => {
  if (ships.length !== SIZES.length || fleetSent) return;
  ws.send(JSON.stringify({
    type: 'place',
    fleet: ships.map((s) => s.cells),
  }));
  fleetSent = true;
  renderPlaceBoard();
  $('placement-status').textContent = 'Флот готов, ждём соперника…';
};

$('btn-again').onclick = () => {
  location.reload();
};

document.addEventListener('keydown', (e) => {
  if (phase === 'placement' && !fleetSent &&
      (e.key === 'r' || e.key === 'R' || e.key === 'к' || e.key === 'К')) {
    horizontal = !horizontal;
    renderPlaceBoard();
  }
});

// ---------- Старт ----------

setupPlacement();
buildBoard($('my-board'), false);
buildBoard($('enemy-board'), true);
showScreen('menu');
