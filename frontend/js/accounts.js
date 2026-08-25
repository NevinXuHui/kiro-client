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
  var card = document.getElementById('outlook-card-input');
  if (card) card.value = '';
}
function closeOutlookModal() {
  var m = document.getElementById('outlook-modal');
  if (m) m.classList.remove('show');
}
function openAddOutlookModal() { openOutlookModal(); }
function closeAddOutlookModal() { closeOutlookModal(); }

// ===== 添加账号（单条） =====
async function addOutlookAccount() {
  var data = ((document.getElementById('outlook-card-input') || {}).value || '').trim();
  if (!data) {
    showToast(_accT('accounts.inputRequired', '请粘贴账号卡密（邮箱----密码----ClientID----RefreshToken）'), 'error');
    return;
  }
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

// ===== 导出账号 =====
function exportOutlookAccounts(format, filter) {
  try {
    var accounts = outlookAllAccounts;

    // 应用过滤器
    if (filter === 'unregistered') {
      accounts = accounts.filter(acc => !acc.registered);
    } else if (filter === 'registered') {
      accounts = accounts.filter(acc => acc.registered);
    } else if (filter === 'success') {
      accounts = accounts.filter(acc => acc.success);
    }

    if (accounts.length === 0) {
      showToast('没有符合条件的账号可导出', 'warning');
      return;
    }

    var filename = 'kiro-outlook-accounts-' + new Date().toISOString().split('T')[0];
    var content = '';

    if (format === 'json') {
      // JSON 格式
      content = JSON.stringify(accounts, null, 2);
      filename += '.json';
      downloadFile(filename, content, 'application/json');
    } else if (format === 'csv') {
      // CSV 格式
      var headers = ['邮箱', '密码', 'ClientID', 'RefreshToken', '已注册', '成功', '添加时间'];
      var rows = [headers.join(',')];

      accounts.forEach(function(acc) {
        var row = [
          csvEscape(acc.email || ''),
          csvEscape(acc.password || ''),
          csvEscape(acc.clientId || ''),
          csvEscape(acc.refreshToken || ''),
          acc.registered ? '是' : '否',
          acc.success ? '是' : '否',
          csvEscape(acc.addedAt || '')
        ];
        rows.push(row.join(','));
      });

      content = rows.join('\n');
      filename += '.csv';
      downloadFile(filename, '﻿' + content, 'text/csv'); // 添加 BOM 支持中文
    } else if (format === 'txt') {
      // TXT 格式（每行一个账号）
      var lines = accounts.map(function(acc) {
        return [acc.email, acc.password, acc.clientId, acc.refreshToken].join('----');
      });
      content = lines.join('\n');
      filename += '.txt';
      downloadFile(filename, content, 'text/plain');
    }

    showToast('导出成功：' + accounts.length + ' 个账号', 'success');
  } catch (e) {
    showToast('导出失败: ' + e.message, 'error');
  }
}

// CSV 字段转义
function csvEscape(str) {
  if (str == null) return '';
  str = String(str);
  if (str.includes(',') || str.includes('"') || str.includes('\n')) {
    return '"' + str.replace(/"/g, '""') + '"';
  }
  return str;
}

// 下载文件
function downloadFile(filename, content, mimeType) {
  var blob = new Blob([content], { type: mimeType });
  var url = URL.createObjectURL(blob);
  var a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.style.display = 'none';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

// ===== 导入账号 =====
function openImportModal() {
  var modal = document.getElementById('import-modal');
  if (modal) {
    modal.classList.add('show');
    // 重置文件输入
    var fileInput = document.getElementById('import-file-input');
    if (fileInput) fileInput.value = '';
    // 隐藏结果
    var result = document.getElementById('import-result');
    if (result) result.style.display = 'none';
  }
}

function closeImportModal() {
  var modal = document.getElementById('import-modal');
  if (modal) modal.classList.remove('show');
}

function triggerFileSelect() {
  var fileInput = document.getElementById('import-file-input');
  if (fileInput) fileInput.click();
}

async function handleFileSelect(event) {
  var file = event.target.files[0];
  if (!file) return;

  try {
    var content = await readFileContent(file);
    await importAccountsFromContent(content, file.name);
  } catch (e) {
    showToast('文件读取失败: ' + e.message, 'error');
  }
}

function readFileContent(file) {
  return new Promise(function(resolve, reject) {
    var reader = new FileReader();
    reader.onload = function(e) { resolve(e.target.result); };
    reader.onerror = function(e) { reject(new Error('文件读取失败')); };
    reader.readAsText(file, 'UTF-8');
  });
}

async function importAccountsFromContent(content, filename) {
  try {
    var accounts = [];
    var errors = [];

    // 检测文件格式
    var ext = filename.split('.').pop().toLowerCase();

    if (ext === 'json') {
      // JSON 格式
      try {
        var data = JSON.parse(content);
        if (Array.isArray(data)) {
          accounts = data;
        } else {
          errors.push('JSON 格式错误：需要是数组');
        }
      } catch (e) {
        errors.push('JSON 解析失败：' + e.message);
      }
    } else if (ext === 'csv') {
      // CSV 格式
      var lines = content.split('\n');
      for (var i = 1; i < lines.length; i++) { // 跳过标题行
        var line = lines[i].trim();
        if (!line) continue;

        var fields = parseCSVLine(line);
        if (fields.length >= 4) {
          accounts.push({
            email: fields[0],
            password: fields[1],
            clientId: fields[2],
            refreshToken: fields[3]
          });
        } else {
          errors.push('第 ' + (i + 1) + ' 行格式错误');
        }
      }
    } else {
      // TXT 格式（每行一个账号）
      var lines = content.split('\n');
      for (var i = 0; i < lines.length; i++) {
        var line = lines[i].trim();
        if (!line || line.startsWith('#') || line.startsWith('//')) continue;

        var parts = line.split('----');
        if (parts.length >= 4) {
          accounts.push({
            email: parts[0].trim(),
            password: parts[1].trim(),
            clientId: parts[2].trim(),
            refreshToken: parts[3].trim()
          });
        } else {
          errors.push('第 ' + (i + 1) + ' 行格式错误');
        }
      }
    }

    // 验证和导入
    var validAccounts = [];
    for (var i = 0; i < accounts.length; i++) {
      var acc = accounts[i];
      if (acc.email && acc.email.includes('@') && acc.password && acc.clientId && acc.refreshToken) {
        validAccounts.push(acc);
      } else {
        errors.push('账号 ' + (i + 1) + ' 缺少必需字段');
      }
    }

    if (validAccounts.length === 0) {
      showImportResult(0, 0, errors);
      return;
    }

    // 批量导入
    var data = validAccounts.map(function(acc) {
      return [acc.email, acc.password, acc.clientId, acc.refreshToken].join('----');
    }).join('\n');

    var result = await window.go.main.App.AddOutlookAccounts(data);

    if (result.error) {
      errors.push(result.error);
    }

    await loadOutlookAccountsList();
    showImportResult(result.added || 0, result.total || 0, errors);

  } catch (e) {
    showToast('导入失败: ' + e.message, 'error');
  }
}

// 解析 CSV 行（处理引号和逗号）
function parseCSVLine(line) {
  var fields = [];
  var current = '';
  var inQuotes = false;

  for (var i = 0; i < line.length; i++) {
    var char = line[i];
    var next = line[i + 1];

    if (char === '"') {
      if (inQuotes && next === '"') {
        current += '"';
        i++; // 跳过下一个引号
      } else {
        inQuotes = !inQuotes;
      }
    } else if (char === ',' && !inQuotes) {
      fields.push(current.trim());
      current = '';
    } else {
      current += char;
    }
  }

  fields.push(current.trim());
  return fields;
}

function showImportResult(added, total, errors) {
  var resultDiv = document.getElementById('import-result');
  if (!resultDiv) return;

  var html = '<div style="padding:16px">';
  html += '<h3 style="margin:0 0 12px 0">导入结果</h3>';
  html += '<div style="margin-bottom:12px">';
  html += '<div>✅ 成功添加: <strong>' + added + '</strong> 个账号</div>';
  html += '<div>📊 当前总数: <strong>' + total + '</strong> 个账号</div>';
  html += '</div>';

  if (errors && errors.length > 0) {
    html += '<div style="margin-top:12px">';
    html += '<div style="color:#f56c6c;font-weight:500">⚠️ 错误信息:</div>';
    html += '<ul style="margin:8px 0;padding-left:20px;max-height:200px;overflow-y:auto">';
    errors.forEach(function(err) {
      html += '<li style="color:#f56c6c">' + escapeHtml(err) + '</li>';
    });
    html += '</ul>';
    html += '</div>';
  }

  html += '</div>';
  resultDiv.innerHTML = html;
  resultDiv.style.display = 'block';

  if (added > 0) {
    showToast('成功导入 ' + added + ' 个账号', 'success');
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

// ===== 导入文件（旧方法，保持兼容） =====
async function importOutlookFile() {
  openImportModal();
}

// ===== 导出菜单控制 =====
function toggleExportMenu(event) {
  event.stopPropagation();
  var menu = document.getElementById('export-menu');
  if (!menu) return;

  var isVisible = menu.style.display !== 'none';
  menu.style.display = isVisible ? 'none' : 'block';

  // 点击其他地方关闭菜单
  if (!isVisible) {
    setTimeout(function() {
      document.addEventListener('click', closeExportMenu);
    }, 0);
  }
}

function closeExportMenu() {
  var menu = document.getElementById('export-menu');
  if (menu) menu.style.display = 'none';
  document.removeEventListener('click', closeExportMenu);
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

// ===== HTTP API 邮箱管理 =====

var httpapiAllAccounts = [];

function openHttpAPIModal() {
  var m = document.getElementById('httpapi-modal');
  if (m) m.classList.add('show');
  var card = document.getElementById('httpapi-card-input');
  if (card) card.value = '';
}
function closeHttpAPIModal() {
  var m = document.getElementById('httpapi-modal');
  if (m) m.classList.remove('show');
}

async function addHttpAPIAccount() {
  var data = ((document.getElementById('httpapi-card-input') || {}).value || '').trim();
  if (!data) {
    showToast(_accT('accounts.httpapiInputRequired', '请粘贴账号（邮箱----API地址）'), 'error');
    return;
  }
  try {
    var result = await window.go.main.App.AddHttpAPIAccounts(data);
    if (result.error) { showToast(result.error, 'error'); return; }
    closeHttpAPIModal();
    await loadHttpAPIAccountsList();
    showToast(_accT('accounts.addedSummary', { n: result.added, total: result.total }, '成功添加 {n} 个账号，当前共 {total} 个'));
  } catch (e) {
    showToast(_accT('toast.addFailed', '添加失败') + ': ' + e.message, 'error');
  }
}

async function importHttpAPIFile() {
  try {
    var path = await window.go.main.App.SelectOutlookFile();
    if (!path) return;
    var result = await window.go.main.App.ImportHttpAPIFile(path);
    if (result.error) { showToast(result.error, 'error'); return; }
    await loadHttpAPIAccountsList();
    showToast(_accT('accounts.importSummary', { n: result.added, total: result.total }, '成功导入 {n} 个账号，当前共 {total} 个'));
  } catch(e) { showToast(_accT('accounts.importFailed', '导入失败') + ': ' + e.message, 'error'); }
}

async function loadHttpAPIAccountsList() {
  try {
    var accounts = await window.go.main.App.GetHttpAPIAccounts();
    httpapiAllAccounts = accounts || [];
    renderHttpAPIPage();
  } catch(e) { console.error('加载 HTTP 邮箱列表失败:', e); }
}

function renderHttpAPIPage() {
  var tbody = document.getElementById('httpapi-account-body');
  var empty = document.getElementById('httpapi-empty');
  var countEl = document.getElementById('httpapi-count');
  var accounts = httpapiAllAccounts || [];

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
      + '<td style="font-family:var(--font-mono);" title="' + escapeHtml(acc.apiUrl) + '">' + escapeHtml(acc.email) + '</td>'
      + '<td>' + badge + '</td>'
      + '<td style="text-align:right;">'
        + '<button onclick="deleteHttpAPIAccount(\'' + escapeHtml(acc.email).replace(/'/g,'\\\'') + '\')" class="btn btn-outline btn-sm" style="color:var(--red);border-color:rgba(229,57,53,0.3);">' + _accT('common.delete', '删除') + '</button>'
      + '</td></tr>';
  });
  if (tbody) tbody.innerHTML = html;
}

async function deleteHttpAPIAccount(email) {
  showConfirmModal(
    _accT('accounts.deleteTitle', '删除账号'),
    _accT('accounts.deleteMsg', { email: email }, '确认删除账号 {email} ?'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.DeleteHttpAPIAccount(email);
        if (result.error) { showToast(result.error, 'error'); return; }
        showToast(_accT('accounts.deletedOne', '账号已删除'));
        await loadHttpAPIAccountsList();
      } catch(e) { showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error'); }
    }
  );
}

function clearAllHttpAPIAccounts() {
  showConfirmModal(
    _accT('accounts.clearAllHttpAPITitle', '清空 HTTP 邮箱'),
    _accT('accounts.clearAllHttpAPIMsg', '确认清空所有 HTTP 邮箱账号？此操作不可恢复！'),
    _accT('accounts.clearAllConfirm', '确认清空'),
    async function() {
      try {
        var result = await window.go.main.App.ClearHttpAPIAccounts();
        if (result.error) { showToast(result.error, 'error'); return; }
        showToast(_accT('accounts.allCleared', '已清空所有账号'));
        await loadHttpAPIAccountsList();
      } catch(e) { showToast(_accT('toast.clearFailed', '清空失败') + ': ' + e.message, 'error'); }
    }
  );
}

function clearRegisteredHttpAPIAccounts() {
  var registered = httpapiAllAccounts.filter(function(a) { return a.registered; }).length;
  if (!registered) { showToast(_accT('accounts.noRegistered', '没有已注册的账号')); return; }
  showConfirmModal(
    _accT('accounts.clearRegisteredTitle', '清除已注册'),
    _accT('accounts.clearRegisteredMsg', { n: registered }, '确认删除 {n} 个已注册（成功/失败）的账号？'),
    _accT('accounts.deleteConfirm', '确认删除'),
    async function() {
      try {
        var result = await window.go.main.App.ClearRegisteredHttpAPIAccounts();
        if (result.error) { showToast(result.error, 'error'); return; }
        showToast(_accT('toast.accountsDeleted', { n: (result.removed || 0) }, '已删除 {n} 个账号'));
        await loadHttpAPIAccountsList();
      } catch(e) { showToast(_accT('toast.deleteFailed', '删除失败') + ': ' + e.message, 'error'); }
    }
  );
}

var httpapiRefreshTimer = null;
function startHttpAPIAutoRefresh() { stopHttpAPIAutoRefresh(); httpapiRefreshTimer = setInterval(loadHttpAPIAccountsList, 3000); }
function stopHttpAPIAutoRefresh() { if (httpapiRefreshTimer) { clearInterval(httpapiRefreshTimer); httpapiRefreshTimer = null; } }

