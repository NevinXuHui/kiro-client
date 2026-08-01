// ===== 任务控制 + 更新系统 + 状态轮询 =====

function _tkT(key, varsOrFallback, fallbackMaybe) {
  var vars = null, fallback = null;
  if (typeof varsOrFallback === 'string') {
    fallback = varsOrFallback;
  } else if (varsOrFallback && typeof varsOrFallback === 'object') {
    vars = varsOrFallback;
    if (typeof fallbackMaybe === 'string') fallback = fallbackMaybe;
  }
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key, vars);
    if (v && v !== key) return v;
  }
  if (fallback != null) {
    if (vars) {
      return fallback.replace(/\{(\w+)\}/g, function(_, k) {
        return vars[k] != null ? vars[k] : '{' + k + '}';
      });
    }
    return fallback;
  }
  return key;
}

function formatTime(seconds) {
  seconds = Math.round(seconds);
  if (seconds < 60) return seconds + 's';
  var m = Math.floor(seconds / 60);
  var s = seconds % 60;
  if (m < 60) return m + 'm ' + s + 's';
  var h = Math.floor(m / 60);
  m = m % 60;
  return h + 'h ' + m + 'm';
}

// 任务模态框（保留兼容，已迁移到注册页面）
function openKiroTaskModal() { switchPage('register'); }
function closeKiroTaskModal() {}

var updateInfo = null;
var _prevRunning = false;
window._kiroLogs = [];

function _escapeLogHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// 将一行日志解析为带高亮 span 的 HTML。
// 识别模式: "HH:MM:SS [prefix] [step] rest"
function _formatLogLine(line) {
  var raw = line.replace(/\r?\n$/, '');
  if (!raw) return '';

  // 整行级别判定 —— 用原始中文判定，避免翻译后关键字缺失
  var low = raw.toLowerCase();
  var cls = 'log-line';
  if (raw.indexOf('注册成功') >= 0 || raw.indexOf('已验活') >= 0 || raw.indexOf('[OK]') >= 0) {
    cls += ' log-line-success';
  } else if (raw.indexOf('失败') >= 0 || raw.indexOf('错误') >= 0 || raw.indexOf('异常') >= 0 ||
             raw.indexOf('被拦截') >= 0 || raw.indexOf('被封') >= 0 ||
             low.indexOf('error') >= 0 || low.indexOf('failed') >= 0) {
    cls += ' log-line-error';
  } else if (raw.indexOf('⚠') >= 0 || raw.indexOf('熔断') >= 0 || raw.indexOf('重试') >= 0) {
    cls += ' log-line-warn';
  } else if (raw.indexOf('[DEBUG]') >= 0) {
    cls += ' log-line-debug';
  }

  // 翻译为当前语言（zh 直接返回原文）
  var display = raw;
  if (window.I18N && typeof window.I18N.translateLog === 'function') {
    display = window.I18N.translateLog(raw);
  }

  // 分段高亮: 时间戳 + [标签] + [step] + 其余
  var html = '';
  var rest = display;

  var m = rest.match(/^(\d{2}:\d{2}:\d{2})\s*/);
  if (m) {
    html += '<span class="log-time">' + _escapeLogHtml(m[1]) + '</span>';
    rest = rest.slice(m[0].length);
  }

  // 匹配若干 [xxx] 前缀，时间戳之后的所有方括号标签
  while (true) {
    var t = rest.match(/^(\[[^\]]+\])\s*/);
    if (!t) break;
    var label = t[1];
    // 纯数字步骤如 [1] [12.5] 用 step 色，其余用 tag 色
    var inner = label.slice(1, -1);
    var isStep = /^\d+(\.\d+)?(\/\d+)?$/.test(inner);
    html += '<span class="' + (isStep ? 'log-step' : 'log-tag') + '">' +
      _escapeLogHtml(label) + '</span>';
    rest = rest.slice(t[0].length);
  }

  html += _escapeLogHtml(rest);
  return '<span class="' + cls + '">' + html + '</span>';
}

function renderUnifiedLogs() {
  var ids = ['masterlog-box', 'register-log-box'];
  for (var i = 0; i < ids.length; i++) {
    var box = document.getElementById(ids[i]);
    if (!box) continue;

    // 首次渲染时挂上行级点击复制（事件委托，后续 innerHTML 重写不会丢）
    if (!box.dataset.copyBound) {
      box.addEventListener('click', function(e) {
        var line = e.target.closest('.log-line');
        if (!line || !box.contains(line)) return;
        var sel = window.getSelection && window.getSelection();
        if (sel && sel.toString().length > 0) return;
        var text = line.textContent.replace(/\u00A0/g, ' ').trim();
        if (!text) return;
        navigator.clipboard.writeText(text).then(function() {
          line.classList.add('log-copied');
          setTimeout(function() { line.classList.remove('log-copied'); }, 600);
          if (typeof showToast === 'function') showToast(_tkT('toast.copied', '复制成功'), 'success');
        }).catch(function(err) {
          if (typeof showToast === 'function') showToast(_tkT('toast.copyFailed', '复制失败') + ': ' + err.message, 'error');
        });
      });
      box.dataset.copyBound = '1';
    }

    var wasAtBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 50;

    var logs = window._kiroLogs || [];
    var html;
    if (!logs.length) {
      html = '<span style="color:var(--text-muted);">' + _tkT('logs.empty', '暂无日志') + '</span>';
    } else {
      html = logs.map(function(l) {
        return _formatLogLine(l.replace(/^\s+/, ''));
      }).join('\n');
    }

    if (box.innerHTML !== html) {
      box.innerHTML = html;
      if (wasAtBottom) box.scrollTop = box.scrollHeight;
    }
  }
}

function copyLogs() {
  // 优先复制 masterlog-box，其次 register-log-box
  var box = document.getElementById('masterlog-box') || document.getElementById('register-log-box');
  if (!box) return;

  var logs = window._kiroLogs || [];
  if (!logs.length) {
    showToast(_tkT('toast.logEmpty', '暂无日志可复制'), 'error');
    return;
  }
  var text = box.textContent;

  navigator.clipboard.writeText(text).then(function() {
    showToast(_tkT('toast.logCopied', '日志已复制到剪贴板'), 'success');
  }).catch(function(e) {
    showToast(_tkT('toast.copyFailed', '复制失败') + ': ' + e.message, 'error');
  });
}

function notifyTaskComplete(taskName, success, failed, total) {
  var msg = _tkT('toast.taskCompleteMsg', { name: taskName, s: success, f: failed, t: total }, '{name} 任务完成！成功 {s} / 失败 {f} / 共 {t}');
  showToast(msg, success > 0 ? 'success' : 'error');
}

async function startTask() {
  try {
    var cfg = getFormConfig();

    if (cfg.useOutlook) {
      saveConfig();
    }

    var result = await window.go.main.App.StartTask(JSON.stringify(cfg));
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }
    updateUIStatus(true);
    showToast(_tkT('toast.taskStarted', '任务已启动'));
  } catch(e) {
    showToast(_tkT('toast.taskStartFailed', '启动失败') + ': ' + e.message, 'error');
  }
}

var _confirmCallback = null;

function showConfirmModal(title, message, btnText, callback) {
  document.getElementById('confirm-title').textContent = title;
  document.getElementById('confirm-message').textContent = message;
  document.getElementById('confirm-action-btn').textContent = btnText || '确认';
  _confirmCallback = callback;
  document.getElementById('confirm-modal').classList.add('show');
}

function closeConfirmModal() {
  document.getElementById('confirm-modal').classList.remove('show');
  _confirmCallback = null;
}

function confirmAction() {
  var cb = _confirmCallback;
  closeConfirmModal();
  if (cb) cb();
}

async function stopTask() {
  try {
    var result = await window.go.main.App.StopTask();
    if (result.error) { 
      showToast(result.error, 'error'); 
      return; 
    }
    document.getElementById('btn-stop').disabled = true;
    showToast(_tkT('toast.taskStopping', '正在停止任务...'));
  } catch(e) {
    showToast(_tkT('toast.taskStopFailed', '停止失败') + ': ' + (e.message || e), 'error');
  }
}

// ===== 手动注册（浏览器）=====

var _manualRegRunning = false;

async function startManualRegister() {
  if (_manualRegRunning) {
    showToast(_tkT('toast.manualRunning', '手动注册已在运行中'), 'error');
    return;
  }
  try {
    var result = await window.go.main.App.StartManualRegister();
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }
    _manualRegRunning = true;
    updateManualRegBadge(true);
    showToast(_tkT('toast.manualStarted', '手动注册已启动，浏览器即将弹出'));
  } catch(e) {
    showToast(_tkT('toast.manualStartFailed', '启动失败') + ': ' + e.message, 'error');
  }
}

function updateManualRegBadge(running) {
  var badge = document.getElementById('manual-reg-badge');
  if (!badge) return;
  var _t = (window.I18N && window.I18N.t) ? window.I18N.t : function(k){return k;};
  badge.textContent = running ? (_t('status.running') || '运行中') : (_t('status.idle') || '空闲');
  badge.className = 'badge ' + (running ? 'db-badge-running' : 'badge-idle');
}

// ===== 更新系统 =====

if (window.runtime) {
  }

// ===== 状态轮询 =====

var lastOutlookUpdate = 0;
setInterval(async function() {
  try {
    var s = await window.go.main.App.GetStatus();
    updateUIStatus(s.running);
    // 注册页状态徽章
    var regBadge = document.getElementById('reg-status-badge');
    if (regBadge) {
      var _tt = (window.I18N && window.I18N.t) ? window.I18N.t : function(k){return k;};
      var rTxt = s.running ? _tt('status.running') : _tt('status.idle');
      if (!rTxt || rTxt === 'status.running' || rTxt === 'status.idle') rTxt = s.running ? '运行中' : '空闲';
      regBadge.textContent = rTxt;
      regBadge.className = 'db-badge ' + (s.running ? 'db-badge-running' : 'db-badge-idle');
    }
    document.getElementById('st-progress').textContent = s.completed + '/' + s.total;
    document.getElementById('st-success').textContent = s.success;
    document.getElementById('st-failed').textContent = s.failed;
    if (s.elapsed > 0) document.getElementById('st-elapsed').textContent = formatTime(s.elapsed);
    var pct = s.total > 0 ? Math.round(s.completed / s.total * 100) : 0;
    document.getElementById('progress-bar').style.width = pct + '%';
    // 检测任务完成
    if (_prevRunning && !s.running && s.completed > 0) {
      notifyTaskComplete('Kiro', s.success, s.failed, s.completed);
    }
    _prevRunning = s.running;
    // 状态指示灯
    var dot = document.getElementById('st-dot');
    if (s.running) { dot.classList.add('running'); } else { dot.classList.remove('running'); }
    // 平均耗时
    var avgEl = document.getElementById('st-avg');
    if (s.completed > 0 && s.elapsed > 0) {
      avgEl.textContent = (s.elapsed / s.completed).toFixed(1) + 's';
    } else {
      avgEl.textContent = '-';
    }
    // 成功率
    var rateEl = document.getElementById('st-rate');
    if (rateEl) {
      if (s.completed > 0) {
        rateEl.textContent = Math.round(s.success / s.completed * 100) + '%';
        rateEl.style.color = s.success > 0 ? 'var(--success)' : 'var(--danger)';
      } else {
        rateEl.textContent = '-';
      }
    }
    // 预计剩余
    var etaEl = document.getElementById('st-eta');
    if (s.running && s.completed > 0 && s.total > s.completed) {
      var avgTime = s.elapsed / s.completed;
      var remaining = (s.total - s.completed) * avgTime;
      etaEl.textContent = formatTime(remaining);
    } else {
      etaEl.textContent = '-';
    }
  } catch(e) {}
  try {
    var kiroLogs = await window.go.main.App.GetLogs() || [];
    window._kiroLogs = kiroLogs;
    renderUnifiedLogs();
  } catch(e) {}
  try {
    var m = await window.go.main.App.GetManualRegisterStatus();
    _manualRegRunning = !!m.running;
    updateManualRegBadge(_manualRegRunning);
  } catch(e) {}

  var now = Date.now();
  if (now - lastOutlookUpdate > 2000) {
    lastOutlookUpdate = now;
    var outlookModal = document.getElementById('outlook-modal');
    if (outlookModal && outlookModal.classList.contains('show')) {
      await loadOutlookAccountsList();
    }
  }
}, 2000);

