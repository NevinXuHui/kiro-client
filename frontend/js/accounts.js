// ===== Outlook 账号管理 =====

function switchAccTab(tabId) {
  document.querySelectorAll('#page-accounts .tab').forEach(function(t) { t.classList.remove('active'); });
  document.querySelectorAll('#page-accounts .tab-content').forEach(function(c) { c.classList.remove('active'); });
  var tab = document.querySelector('#page-accounts .tab[data-tab="' + tabId + '"]');
  if (tab) tab.classList.add('active');
  var content = document.getElementById('tab-' + tabId);
  if (content) content.classList.add('active');
}

var outlookAllAccounts = [];

function _accT(key, varsOrFallback, fallbackMaybe) {
  var vars = null, fallback = null;
  if (typeof varsOrFallback === 'string') fallback = varsOrFallback;
  else if (varsOrFallback && typeof varsOrFallback === 'object') { vars = varsOrFallback; if (typeof fallbackMaybe === 'string') fallback = fallbackMaybe; }
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key, vars);
    if (v && v !== key) return v;
  }
  if (fallback != null) {
    if (vars) return fallback.replace(/\{(\w+)\}/g, function(_, k) { return vars[k] != null ? vars[k] : '{' + k + '}'; });
    return fallback;
  }
  return key;
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ===== Modal 控制 =====
function openOutlookModal() {
  var m = document.getElementById('outlook-modal');
  if (m) m.classList.add('show');
  var em = document.getElementById('outlook-email-input');
  if (em) em.value = '';
  var pwd = document.getElementById('outlook-pwd-input');
  if (pwd) pwd.value = '';
}
function closeOutlookModal() {
  var m = document.getElementById('outlook-modal');
  if (m) m.classList.remove('show');
}
function openAddOutlookModal() { openOutlookModal(); }
function closeAddOutlookModal() { closeOutlookModal(); }

// ===== 添加账号（单条） =====
async function addOutlookAccount() {
  var email = (document.getElementById('outlook-email-input').value || '').trim();
  var pwd = (document.getElementById('outlook-pwd-input').value || '').trim();
  if (!email || !pwd) {
    showToast(_accT('accounts.inputRequired', '请填写邮箱和密码'), 'error');
    return;
  }
  var data = email + '----' + pwd;
  try {
    var result = await window.go.main.App.AddOutlookAccounts(data);
    if (result.error) { showToast(result.error, 'error'); return; }
    closeOutlookModal();
    await loadOutlookAccountsList();
    showToast(_accT('accounts.addedSummary', { n: result.added, total: result.total }, '成功添加 {n} 个账号，当前共 {total} 个'));
  } catch (e) {
    showToast(_accT('toast.addFailed', '添加失败') + ': ' + e.message, 'error');
  }
}

// ===== 多账号批量添加（旧版兼容） =====
async function addOutlookAccounts() {
  var data = (document.getElementById('cfg-outlook-data') && document.getElementById('cfg-outlook-data').value) || '';
  if (!data) { showToast(_accT('accounts.inputRequired', '请先输入 Outlook 账号数据'), 'error'); return; }
  try {
    var result = await window.go.main.App.AddOutlookAccounts(data);
    if (result.error) { showToast(result.error, 'error'); return; }
    closeOutlookModal();
    await loadOutlookAccountsList();
    showToast(_accT('accounts.addedSummary', { n: result.added, total: result.total }, '成功添加 {n} 个账号，当前共 {total} 个'));
  } catch(e) { showToast(_accT('toast.addFailed', '添加失败') + ': ' + e.message, 'error'); }
}

async function importOutlookFile() {
  try {
    var path = await window.go.main.App.SelectOutlookFile();
    if (!path) return;
    var result = await window.go.main.App.ImportOutlookFile(path);
    if (result.error) { showToast(result.error, 'error'); return; }
    await loadOutlookAccountsList();
    showToast(_accT('accounts.importSummary', { n: result.added, total: result.total }, '成功导入 {n} 个账号，当前共 {total} 个'));
  } catch(e) { showToast(_accT('accounts.importFailed', '导入失败') + ': ' + e.message, 'error'); }
}

// ===== 加载/渲染 =====
async function loadOutlookAccountsList() {
  try {
    var accounts = await window.go.main.App.GetOutlookAccounts();
    outlookAllAccounts = accounts || [];
    renderOutlookPage();
  } catch(e) { console.error('加载账号列表失败:', e); }
}

function renderOutlookPage() {
  var tbody = document.getElementById('outlook-account-body');
  var empty = document.getElementById('outlook-empty');
  var countEl = document.getElementById('outlook-count');
  var accounts = outlookAllAccounts || [];

  if (countEl) countEl.textContent = accounts.length + ' ' + _accT('accounts.unit', '个');

  if (!accounts.length) {
    if (tbody) tbody.innerHTML = '';
    if (empty) empty.style.display = 'flex';
    return;
  }
  if (empty) empty.style.display = 'none';

  var html = '';
  accounts.forEach(function(acc, i) {
    var badge = '';
    if (acc.registered && acc.success) badge = '<span class="badge badge-ok">' + _accT('status.success', '成功') + '</span>';
    else if (acc.registered && !acc.success) badge = '<span class="badge badge-err">' + _accT('status.failed', '失败') + '</span>';
    else badge = '<span class="badge badge-idle">' + _accT('status.unregistered', '待注册') + '</span>';

    html += '<tr>'
      + '<td style="font-family:var(--font-mono);color:var(--text-3);">' + (i+1) + '</td>'
      + '<td style="font-family:var(--font-mono);">' + escapeHtml(acc.email) + '</td>'
      + '<td>' + badge + '</td>'
      + '<td style="text-align:right;">'
        + '<button onclick="deleteOutlookAccount(\'' + escapeHtml(acc.email).replace(/'/g,'\\\'') + '\')" class="btn btn-outline btn-sm" style="color:var(--red);border-color:rgba(229,57,53,0.3);">' + _accT('common.delete', '删除') + '</button>'
      + '</td></tr>';
  });
  if (tbody) tbody.innerHTML = html;
}

// ===== 删除/清空 =====
async function deleteOutlookAccount(email) {
  showConfirmModal(
    _accT('accounts.deleteTitle', '删除账号'),
    _accT('accounts.deleteMsg', { email: email }, '确认删除账号 {email} ?'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.DeleteOutlookAccount(email);
        if (result.error) { showToast(result.error, 'error'); return; }
        showToast(_accT('accounts.deletedOne', '账号已删除'));
        await loadOutlookAccountsList();
      } catch(e) { showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error'); }
    }
  );
}

function clearAllOutlookAccounts() {
  showConfirmModal(
    _accT('accounts.clearAllTitle', '清空 Outlook 账号'),
    _accT('accounts.clearAllMsg', '确认清空所有 Outlook 账号？此操作不可恢复！'),
    _accT('accounts.clearAllConfirm', '确认清空'),
    async function() {
      try {
        var result = await window.go.main.App.ClearOutlookAccounts();
        if (result.error) { showToast(result.error, 'error'); return; }
        showToast(_accT('accounts.allCleared', '已清空所有账号'));
        await loadOutlookAccountsList();
      } catch(e) { showToast(_accT('toast.clearFailed', '清空失败') + ': ' + e.message, 'error'); }
    }
  );
}

function clearRegisteredOutlookAccounts() {
  var registered = outlookAllAccounts.filter(function(a) { return a.registered; }).length;
  if (!registered) { showToast(_accT('accounts.noRegistered', '没有已注册的账号')); return; }
  showConfirmModal(
    _accT('accounts.clearRegisteredTitle', '清除已注册'),
    _accT('accounts.clearRegisteredMsg', { n: registered }, '确认删除 {n} 个已注册（成功/失败）的账号？'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.ClearRegisteredOutlookAccounts();
        if (result.error) { showToast(result.error, 'error'); return; }
        showToast(_accT('toast.accountsDeleted', { n: (result.removed || 0) }, '已删除 {n} 个账号'));
        await loadOutlookAccountsList();
      } catch(e) { showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error'); }
    }
  );
}

// ===== 自动刷新 =====
var outlookRefreshTimer = null;
function startOutlookAutoRefresh() { stopOutlookAutoRefresh(); outlookRefreshTimer = setInterval(loadOutlookAccountsList, 3000); }
function stopOutlookAutoRefresh() { if (outlookRefreshTimer) { clearInterval(outlookRefreshTimer); outlookRefreshTimer = null; } }

