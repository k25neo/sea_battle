'use strict';

const SIZE = 10;
const SIZES = [4, 3, 3, 2, 2, 2, 1, 1, 1, 1];
const LETTERS = 'АБВГДЕЖЗИК';
const SS_KEY = '***'; // сессия: переживает перезагрузку вкладки

let ws = null;
let side = -1;
let oppName = '';
let myTurn = false;
let phase = 'menu'; // menu | placement | battle

// Сессия игрока {token, code} — по ней сервер пускает обратно в комнату
// после обрыва (телефон рвёт WebSocket, когда браузер сворачивают).
let session = null;
try { session = JSON.parse(sessionStorage.getItem(SS_KEY) || 'null'); } catch (e) {}

function saveSession(s) {
  session = s;
  if (s) sessionStorage.setItem(SS_KEY, JSON.stringify(s));
  else sessionStorage.removeItem(SS_KEY);
}

let reconnectAttempts = 0;
let reconnectTimer = null;
let rejoinPending = false;
const MAX_RECONNECTS = 40;

function stopReconnect() {
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  reconnectAttempts = 0;
}

// --- Расстановка ---
let ships = [];        // [{sizeIdx, cells}]
let horizontal = true;
let fleetSent = false;
let quickMatch = false; // вход по «Найти бой» (публичная комната)

// --- Бой ---
let myMarks = {};      // 'x,y' -> miss|hit|sunk|area — выстрелы по мне
let enemyMarks = {};   // 'x,y' -> miss|hit|sunk|area — мои выстрелы

const $ = (id) => document.getElementById(id);
const key = (x, y) => x + ',' + y;

// Вход по ссылке вида …?room=КОД — гость играет без ручного ввода кода.
const inviteCode = (function () {
  const p = new URLSearchParams(location.search).get('room');
  if (p) history.replaceState(null, '', location.pathname); // неjoining на reload
  return p ? p.trim().toUpperCase() : null;
})();
let joinViaLink = false;

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
    for (const c of msg.area) {
      const k = key(c.x, c.y);
      if (!marks[k]) marks[k] = 'area';
    }
  } else {
    marks[key(msg.x, msg.y)] = msg.hit ? 'hit' : 'miss';
  }
}

function setStatus(text) {
  $('battle-status').textContent = text;
}

function setPlacementStatus(text) {
  $('placement-status').textContent = text;
}

function showOpponent(name) {
  oppName = name || '';
  $('opp-name-own').textContent = oppName ? `(${oppName})` : '';
  $('opp-name-enemy').textContent = oppName ? `(${oppName})` : '';
}

// ---------- Сообщения сервера ----------

function handleMessage(msg) {
  switch (msg.type) {
    case 'room':
      side = msg.side;
      rejoinPending = false;
      stopReconnect();
      saveSession({ token: msg.token, code: msg.code });
      $('room-code').textContent = msg.code;
      // Ссылка-приглашение для соперника.
      $('invite-link').value = location.origin + location.pathname + '?room=' + msg.code;
      if (navigator.share) $('btn-share').classList.remove('hidden');
      if (phase !== 'battle') {
        phase = 'placement';
        fleetSent = false;
        ships = [];
        myMarks = {};
        enemyMarks = {};
        showOpponent('');
        $('open-rooms').classList.add('hidden');
        setPlacementStatus(quickMatch ? 'Ищем соперника…' : 'Ожидание второго игрока…');
        showScreen('placement');
        renderPlaceBoard();
      }
      break;

    case 'rooms':
      if (phase === 'placement' && oppName === '') renderOpenRooms(msg.open);
      break;

    case 'opponent':
      showOpponent(msg.name);
      $('open-rooms').classList.add('hidden');
      if (phase === 'placement' && !fleetSent) {
        setPlacementStatus(`${msg.name} в сети — расставьте корабли`);
      }
      break;

    case 'placed':
      setPlacementStatus('Флот готов, ждём соперника…');
      break;

    case 'snapshot':
      applySnapshot(msg);
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

    case 'opponent_off':
      flashToast('Связь с соперником оборвалась, ждём возврата…');
      if (phase === 'placement') setPlacementStatus('Соперник отключился, ждём…');
      if (phase === 'battle') setStatus('Соперник отключился, ждём до 5 минут…');
      break;

    case 'opponent_back':
      flashToast('Соперник снова в сети');
      if (phase === 'battle') setStatus(myTurn ? 'Ваш ход' : `Ход ${oppName || 'соперника'}…`);
      break;

    case 'game_over':
      phase = 'menu';
      stopReconnect();
      saveSession(null);
      showOverlay(msg.youWon ? '🏆 Победа!' : '💥 Поражение',
        msg.youWon ? 'Весь флот противника потоплен.' : 'Ваш флот уничтожен.');
      break;

    case 'opponent_left':
      phase = 'menu';
      stopReconnect();
      saveSession(null);
      showOverlay('Соперник отключился', 'Игра прервана.');
      break;

    case 'error':
      if (rejoinPending) {
        rejoinPending = false;
        sessionGone();
        break;
      }
      if (joinViaLink) {
        joinViaLink = false;
        showScreen('menu');
        $('join-code').value = inviteCode || '';
        flashToast(msg.message + ' — попробуйте ввести код вручную');
        break;
      }
      flashToast(msg.message);
      if (msg.message === 'сейчас не ваш ход') {
        myTurn = false;
      }
      break;
  }
}

// Восстановление состояния после переподключения.
function applySnapshot(msg) {
  showOpponent(msg.opponent);
  if (msg.phase === 'finished') {
    phase = 'menu';
    stopReconnect();
    saveSession(null);
    showOverlay('Игра завершена', 'Пока вы были offline, партия закончилась.');
    return;
  }
  if (msg.phase === 'playing') {
    // Восстанавливаем свои корабли (sizeIdx подбираем по длине).
    const rebuilt = [];
    for (const cells of msg.fleet || []) {
      const idx = SIZES.findIndex(
        (n, i) => n === cells.length && !rebuilt.some((s) => s.sizeIdx === i));
      rebuilt.push({ sizeIdx: idx === -1 ? 0 : idx, cells });
    }
    ships = rebuilt;
    myMarks = marksToObj(msg.board);
    mergeArea(myMarks, msg.area);
    enemyMarks = marksToObj(msg.enemy);
    mergeArea(enemyMarks, msg.enemyArea);
    myTurn = msg.youTurn;
    phase = 'battle';
    buildBoard($('my-board'), false);
    buildBoard($('enemy-board'), true);
    showScreen('battle');
    renderBoards();
    setStatus(myTurn ? 'Ваш ход — стреляйте по полю противника' : `Ход ${oppName || 'соперника'}…`);
    return;
  }
  // waiting | placing
  phase = 'placement';
  fleetSent = !!msg.ready;
  if (msg.fleet && msg.fleet.length) {
    const rebuilt = [];
    for (const cells of msg.fleet) {
      const idx = SIZES.findIndex(
        (n, i) => n === cells.length && !rebuilt.some((s) => s.sizeIdx === i));
      rebuilt.push({ sizeIdx: idx === -1 ? 0 : idx, cells });
    }
    ships = rebuilt;
  }
  showScreen('placement');
  renderPlaceBoard();
  if (fleetSent) setPlacementStatus('Флот готов, ждём соперника…');
  else setPlacementStatus(msg.opponent ? `${msg.opponent} в сети — расставьте корабли` : 'Ожидание второго игрока…');
}

function marksToObj(marks) {
  const o = {};
  for (const m of marks || []) {
    o[key(m.x, m.y)] = m.sunk ? 'sunk' : m.hit ? 'hit' : 'miss';
  }
  return o;
}

function mergeArea(marks, area) {
  for (const c of area || []) {
    const k = key(c.x, c.y);
    if (!marks[k]) marks[k] = 'area';
  }
}

// ---------- Сеть ----------

function connect(sendObj) {
  rejoinPending = sendObj.type === 'rejoin';
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
    if (phase === 'menu' && !session) return;
    if (session && session.token && reconnectAttempts < MAX_RECONNECTS) {
      scheduleReconnect();
    } else if (phase !== 'menu') {
      giveUp('Соединение потеряно', 'Не удалось переподключиться. Начните новую игру.');
    }
  };
}

function scheduleReconnect() {
  const delay = Math.min(1000 * Math.pow(1.6, reconnectAttempts), 8000);
  reconnectAttempts++;
  if (reconnectAttempts === 1) flashToast('Связь потеряна — переподключение…');
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => {
    if (!session || !session.token) return;
    connect({ type: 'rejoin', token: session.token });
  }, delay);
}

function giveUp(title, text) {
  phase = 'menu';
  stopReconnect();
  saveSession(null);
  showOverlay(title, text);
}

// Сервер отказал в переподключении (комната удалена/игра закончилась).
function sessionGone() {
  giveUp('Игра не найдена', 'Сессия истекла — начните новую игру.');
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

$('btn-copy').onclick = async () => {
  const link = $('invite-link').value;
  try {
    await navigator.clipboard.writeText(link);
    flashToast('Ссылка скопирована');
  } catch (e) {
    // fallback для старых браузеров
    const el = $('invite-link');
    el.select();
    document.execCommand('copy');
    flashToast('Ссылка скопирована');
  }
};

$('btn-share').onclick = () => {
  const link = $('invite-link').value;
  navigator.share({
    title: 'Морской бой',
    text: `Заходи в бой, код комнаты ${(session && session.code) || $('room-code').textContent}:`,
    url: link,
  }).catch(() => {});
};

$('btn-find').onclick = () => {
  const name = $('name-input').value.trim() || 'Игрок';
  quickMatch = true;
  connect({ type: 'find', name });
};

$('btn-create').onclick = () => {
  const name = $('name-input').value.trim() || 'Игрок';
  quickMatch = false;
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
  setPlacementStatus('Флот готов, ждём соперника…');
};

$('btn-again').onclick = () => {
  saveSession(null);
  location.reload();
};

document.addEventListener('keydown', (e) => {
  if (phase === 'placement' && !fleetSent &&
      (e.key === 'r' || e.key === 'R' || e.key === 'к' || e.key === 'К')) {
    horizontal = !horizontal;
    renderPlaceBoard();
  }
});

// ---------- Открытые комнаты (быстрый подбор) ----------

function renderOpenRooms(list) {
  const box = $('open-rooms'), inner = $('open-rooms-list');
  if (phase !== 'placement') { box.classList.add('hidden'); return; }
  box.classList.remove('hidden');
  inner.innerHTML = '';
  if (!list || !list.length) {
    const d = document.createElement('div');
    d.className = 'or-empty';
    d.textContent = 'Пока никто не ждёт — ждите здесь, соперник появится в этом списке.';
    inner.appendChild(d);
    return;
  }
  for (const r of list) {
    const b = document.createElement('button');
    b.textContent = `${r.code} · ${r.host}`;
    b.onclick = () => leaveToJoin(r.code);
    inner.appendChild(b);
  }
}

// Уходим из своей комнаты в чужую (свой слот просто истечёт по таймауту).
function leaveToJoin(code) {
  stopReconnect();
  saveSession(null);
  phase = 'menu'; // чтобы onclose старого соединения ничего не показал
  if (ws) {
    try { ws.close(); } catch (e) {}
  }
  quickMatch = false;
  connect({ type: 'join', code, name: $('name-input').value.trim() || 'Игрок' });
}

// ---------- Статистика сервера ----------

async function loadStats() {
  try {
    const resp = await fetch(new URL('api/stats', location.href).href);
    const s = await resp.json();
    const parts = [`👥 ${s.playersOnline} в сети`, `⚔️ боёв сейчас: ${s.playing}`];
    if (s.waiting > 0) parts.push(`⏳ ждут соперника: ${s.waiting}`);
    if (s.gamesFinished > 0) parts.push(`сыграно партий: ${s.gamesFinished}`);
    $('stats-line').textContent = parts.join(' · ');
  } catch (e) {
    $('stats-line').textContent = '';
  }
}

// ---------- Старт ----------

setupPlacement();
buildBoard($('my-board'), false);
buildBoard($('enemy-board'), true);
showScreen('menu');

// Если вкладка пережила перезагрузку (телефон «выбросил» страницу) —
// сразу пробуем вернуться в комнату. Если открыта ссылка-приглашение —
// гость входит в комнату автоматически.
if (session && session.token) {
  connect({ type: 'rejoin', token: session.token });
} else if (inviteCode) {
  joinViaLink = true;
  connect({ type: 'join', code: inviteCode, name: 'Игрок' });
} else {
  showScreen('menu');
}

loadStats();
setInterval(loadStats, 30000);

// Пока ждём соперника в лобби — освежаем витрину открытых комнат.
setInterval(() => {
  if (phase === 'placement' && oppName === '' &&
      ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'list' }));
  }
}, 5000);
