// ===== 概览页面逻辑 =====

var overviewTimer = null;
var taskStatusTimer = null;

function _ovT(key, fallback) {
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key);
    if (v && v !== key) return v;
  }
  return fallback;
}

// loadOverview 加载概览数据（含账号池统计，3秒刷新）
async function loadOverview() {
  if (!window.go || !window.go.main || !window.go.main.App) return;
  try {
    var data = await window.go.main.App.GetOverview();
    updateOverviewUI(data);
  } catch (e) {
    console.error('加载概览数据失败:', e);
  }
}

// loadTaskStatus 加载实时任务状态（纯内存，1秒刷新）
async function loadTaskStatus() {
  if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.GetTaskStatus) return;
  try {
    var data = await window.go.main.App.GetTaskStatus();
    updateTaskStatusUI(data);
  } catch (e) {}
}

// updateOverviewUI 更新概览界面
function updateOverviewUI(data) {
  var kiro = data.kiro || {};

  // Kiro 状态徽章
  var kiroStatusEl = document.getElementById('ov-kiro-status');
  if (kiroStatusEl) {
    if (kiro.taskRunning) {
      kiroStatusEl.textContent = _ovT('status.running', '运行中');
      kiroStatusEl.className = 'badge db-badge-running';
    } else {
      kiroStatusEl.textContent = _ovT('status.idle', '空闲');
      kiroStatusEl.className = 'badge badge-idle';
    }
  }

  // 号池统计（来自 pool.GetManager）
  var pool = data.pool || {};
  setText('ov-pool-total', pool.total || 0);
  setText('ov-pool-healthy', pool.healthy || 0);
  setText('ov-pool-unhealthy', pool.unhealthy || 0);
  setText('ov-pool-models', pool.models || 0);

  // 本次任务成功数
  var taskSuccess = kiro.taskSuccess || 0;
  var taskFailed = kiro.taskFailed || 0;
  var taskTotal = taskSuccess + taskFailed;
  var successRate = taskTotal > 0 ? Math.round(taskSuccess / taskTotal * 100) : 0;
}

// 辅助函数
function setText(id, text) {
  var el = document.getElementById(id);
  if (el) el.textContent = text;
}

function setWidth(id, width) {
  var el = document.getElementById(id);
  if (el) el.style.width = width;
}

// 更新任务状态卡片（从快速轮询）
function updateTaskStatusUI(data) {
  var kiro = data.kiro || {};
  renderOverviewFeed(!!kiro.taskRunning);
  var kiroStatusEl = document.getElementById('ov-kiro-status');
  if (!kiroStatusEl) return;
  if (kiro.taskRunning) {
    kiroStatusEl.textContent = _ovT('status.running', '运行中');
    kiroStatusEl.className = 'badge db-badge-running';
  } else {
    kiroStatusEl.textContent = _ovT('status.idle', '空闲');
    kiroStatusEl.className = 'badge badge-idle';
  }
}

// ===== 右侧「实时动态」信息栏 =====
// 数据源复用 window._kiroLogs（与总日志同一份内存日志），仅做展示聚合。
var OV_FEED_MAX = 14;

var _OV_ICONS = {
  ok: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m8.5 12.5 2.5 2.5 4.5-5"/></svg>',
  err: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m9 9 6 6M15 9l-6 6"/></svg>',
  warn: '<svg viewBox="0 0 24 24"><path d="M10.3 4.3 2.5 18a1.6 1.6 0 0 0 1.4 2.4h16.2a1.6 1.6 0 0 0 1.4-2.4L13.7 4.3a1.6 1.6 0 0 0-2.8 0z"/><path d="M12 9.5v4M12 17.2h.01"/></svg>',
  run: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7.6V12l3.2 2"/></svg>',
  info: '<svg viewBox="0 0 24 24"><path d="M20.6 14.2a2.2 2.2 0 0 1-2.2 2.2H7.6L3.4 20.6V5.6a2.2 2.2 0 0 1 2.2-2.2h12.8a2.2 2.2 0 0 1 2.2 2.2z"/></svg>'
};

function _ovEsc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// 把一行原始日志拆成 { time, text, kind }
function _ovParseLine(raw) {
  var line = String(raw || '').replace(/^\s+/, '').replace(/\r?\n$/, '');
  var time = '';
  var m = line.match(/^(\d{2}:\d{2}:\d{2})\s*/);
  if (m) { time = m[1]; line = line.slice(m[0].length); }

  var low = line.toLowerCase();
  var kind = 'info';
  if (line.indexOf('注册成功') >= 0 || line.indexOf('已验活') >= 0 || line.indexOf('[OK]') >= 0) {
    kind = 'ok';
  } else if (line.indexOf('失败') >= 0 || line.indexOf('错误') >= 0 || line.indexOf('异常') >= 0 ||
             line.indexOf('被拦截') >= 0 || line.indexOf('被封') >= 0 ||
             low.indexOf('error') >= 0 || low.indexOf('failed') >= 0) {
    kind = 'err';
  } else if (line.indexOf('⚠') >= 0 || line.indexOf('熔断') >= 0 || line.indexOf('重试') >= 0) {
    kind = 'warn';
  }

  // 去掉所有 [xxx] 前缀标签，保留正文
  var tags = [];
  while (true) {
    var t = line.match(/^(\[[^\]]+\])\s*/);
    if (!t) break;
    tags.push(t[1].slice(1, -1));
    line = line.slice(t[0].length);
  }

  var text = line.trim();
  if (!text && tags.length) text = tags[tags.length - 1];
  if (window.I18N && typeof window.I18N.translateLog === 'function') {
    text = window.I18N.translateLog(text);
  }
  return { time: time, text: text, kind: kind, tag: tags.length ? tags[0] : '' };
}

function renderOverviewFeed(isRunning) {
  var box = document.getElementById('ov-feed');
  if (!box) return;

  var logs = window._kiroLogs || [];
  var items = [];
  for (var i = logs.length - 1; i >= 0 && items.length < OV_FEED_MAX; i--) {
    var it = _ovParseLine(logs[i]);
    if (!it.text) continue;
    items.push(it);
  }

  var countEl = document.getElementById('ov-feed-count');
  if (countEl) countEl.textContent = logs.length;

  if (!items.length) {
    box.innerHTML = '<div class="empty" style="padding:36px 10px;">' +
      '<p>' + _ovEsc(_ovT('logs.empty', '暂无日志')) + '</p>' +
      '<p class="hint">' + _ovEsc(_ovT('overview.activityHint', '任务运行后这里会显示实时动态')) + '</p>' +
      '</div>';
    return;
  }

  var html = '<div class="ov-grp">' + _ovEsc(_ovT('overview.latest', '最新')) + '</div>';
  for (var j = 0; j < items.length; j++) {
    var d = items[j];
    var hot = (j === 0 && isRunning);
    var icon = _OV_ICONS[hot ? 'run' : d.kind] || _OV_ICONS.info;
    html += '<div class="fd' + (hot ? ' on' : '') + '">' +
      '<span class="fd-tile">' + icon + '</span>' +
      '<span class="fd-txt">' +
        '<i class="fd-t" title="' + _ovEsc(d.text) + '">' + _ovEsc(d.text) + '</i>' +
        '<i class="fd-s">' + _ovEsc(d.time || '--:--:--') + (d.tag ? ' · ' + _ovEsc(d.tag) : '') + '</i>' +
      '</span>' +
      (hot ? '<span class="fd-go"><svg viewBox="0 0 24 24"><path d="M4.6 12h14.8M13.4 6l6 6-6 6"/></svg></span>' : '') +
      '</div>';
    if (j === 0) {
      html += '<div class="ov-grp">' + _ovEsc(_ovT('overview.earlier', '更早')) + '</div>';
    }
  }
  box.innerHTML = html;
}

// 启动概览定时刷新
function startOverviewTimer() {
  if (overviewTimer) clearInterval(overviewTimer);
  if (taskStatusTimer) clearInterval(taskStatusTimer);
  renderOverviewFeed(false);
  loadOverview();
  loadTaskStatus();
  overviewTimer = setInterval(loadOverview, 3000);
  taskStatusTimer = setInterval(loadTaskStatus, 1000);
}

// 停止概览定时刷新
function stopOverviewTimer() {
  if (overviewTimer) {
    clearInterval(overviewTimer);
    overviewTimer = null;
  }
  if (taskStatusTimer) {
    clearInterval(taskStatusTimer);
    taskStatusTimer = null;
  }
}

// 首屏先渲染一次右侧动态栏（概览页默认 active，不依赖 Wails runtime）
window.addEventListener('DOMContentLoaded', function() {
  renderOverviewFeed(false);
});
