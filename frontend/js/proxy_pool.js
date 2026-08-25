// ===== 代理池（行内编辑式） =====
// 设计：默认显示一个空输入框；URL 留空=直连。点击「+ 添加代理」追加新行；
// 失焦/回车时持久化。同一时刻可以有多个空行，但保存时空 URL 行不会落库。

var proxyPool = [];        // 来自后端的真实条目（含 id）
var pendingEmptyRows = 1;  // 还未保存的空行数（最少 1 个）
var selectedProxyIds = {}; // id -> true
var proxyTestStatus = {};  // id -> { ok, text }
var proxyBatchTesting = false;

function escapeProxyHtml(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, function(c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

async function loadProxyPool() {
  try {
    var list = await window.go.main.App.ListProxyPool();
    proxyPool = list || [];
  } catch (e) {
    proxyPool = [];
  }
  // 若已有保存条目，就不再强制显示空行；没有时保留 1 个空行
  pendingEmptyRows = proxyPool.length ? 0 : 1;
  var alive = {};
  for (var i = 0; i < proxyPool.length; i++) {
    if (selectedProxyIds[proxyPool[i].id]) alive[proxyPool[i].id] = true;
  }
  selectedProxyIds = alive;
  renderProxyPool();
}

function renderProxyPool() {
  var box = document.getElementById('proxy-pool-list');
  if (!box) return;

  // 计算预估命中率（多于 1 条时显示百分比）
  var multi = (proxyPool.length + pendingEmptyRows) > 1;
  var totalSoft = 0;
  var soft = proxyPool.map(function(p) {
    if (!p.url) return 0;
    var w = p.weight > 0 ? p.weight : 1;
    return Math.pow(w, 0.6);
  });
  for (var i = 0; i < soft.length; i++) totalSoft += soft[i];

  var html = '';

  // 已保存条目
  for (var idx = 0; idx < proxyPool.length; idx++) {
    var p = proxyPool[idx];
    var pct = (multi && totalSoft > 0) ? (Math.round(soft[idx] / totalSoft * 1000) / 10) : null;
    var st = proxyTestStatus[p.id];
    var stColor = 'var(--text-3)';
    if (st) {
      if (st.pending) stColor = 'var(--text-3)';
      else stColor = st.ok ? 'var(--success, #16a34a)' : 'var(--danger)';
    }
    var stHtml = st
      ? '<span style="font-size:11px;min-width:52px;color:' + stColor + ';">' + escapeProxyHtml(st.text) + '</span>'
      : '';
    html += (
      '<div style="display:flex;align-items:center;gap:6px;margin-bottom:6px;">' +
        '<input type="checkbox" class="proxy-row-chk" data-id="' + escapeProxyHtml(p.id) + '" ' + (selectedProxyIds[p.id] ? 'checked' : '') + ' onchange="toggleProxyRow(\'' + p.id + '\', this.checked)" style="width:14px;height:14px;accent-color:var(--blue);flex-shrink:0;">' +
        '<input type="text" value="' + escapeProxyHtml(p.url) + '" placeholder="留空=直连" onchange="updateProxyEntryURL(\'' + p.id + '\', this.value)" class="form-input" style="flex:1;font-family:var(--font-mono);font-size:12px;">' +
        '<input type="number" min="1" max="100" value="' + (p.weight || 1) + '" title="权重 1-100" onchange="updateProxyEntry(\'' + p.id + '\', \'weight\', this.value)" style="width:54px;text-align:center;padding:4px;border:1px solid var(--border);border-radius:4px;background:var(--bg-subtle);font-size:12px;">' +
        (pct != null ? '<span style="font-size:11px;color:var(--text-muted);min-width:42px;text-align:right;">' + pct + '%</span>' : '') +
        stHtml +
        '<button type="button" onclick="testProxyEntryByIdx(' + idx + ')" class="btn btn-secondary btn-sm">测试</button>' +
        '<button type="button" onclick="deleteProxyEntry(\'' + p.id + '\')" class="btn btn-secondary btn-sm" style="color:var(--danger);">删除</button>' +
      '</div>'
    );
  }

  // 未保存的空行
  for (var j = 0; j < pendingEmptyRows; j++) {
    var rowIdx = j;
    html += (
      '<div data-pending-idx="' + rowIdx + '" style="display:flex;align-items:center;gap:6px;margin-bottom:6px;">' +
        '<input type="text" placeholder="留空=直连，或填入代理地址" onblur="savePendingProxyRow(' + rowIdx + ', this.value)" onkeydown="if(event.key===\'Enter\'){this.blur();}" class="form-input" style="flex:1;font-family:var(--font-mono);font-size:12px;">' +
        '<input type="number" min="1" max="100" value="1" data-pending-weight="' + rowIdx + '" title="权重 1-100" style="width:54px;text-align:center;padding:4px;border:1px solid var(--border);border-radius:4px;background:var(--bg-subtle);font-size:12px;">' +
        (proxyPool.length + pendingEmptyRows > 1
          ? '<button type="button" onclick="removePendingProxyRow(' + rowIdx + ')" class="btn btn-secondary btn-sm">移除</button>'
          : '') +
      '</div>'
    );
  }

  box.innerHTML = html;
  syncProxySelectAllUI();
}

function addEmptyProxyRow() {
  pendingEmptyRows++;
  renderProxyPool();
  // 把焦点放到新追加的行
  setTimeout(function() {
    var box = document.getElementById('proxy-pool-list');
    if (!box) return;
    var rows = box.querySelectorAll('[data-pending-idx] input[type="text"]');
    if (rows.length) rows[rows.length - 1].focus();
  }, 0);
}

function showBatchAddProxyModal() {
  var modal = document.getElementById('batch-proxy-modal');
  var textarea = document.getElementById('batch-proxy-input');
  var weightInput = document.getElementById('batch-proxy-weight');
  if (!modal) return;
  if (textarea) textarea.value = '';
  if (weightInput) weightInput.value = '50';
  modal.classList.add('show');
  setTimeout(function() {
    if (textarea) textarea.focus();
  }, 0);
}

function closeBatchAddProxyModal() {
  var modal = document.getElementById('batch-proxy-modal');
  if (modal) modal.classList.remove('show');
}

async function confirmBatchAddProxy() {
  var textarea = document.getElementById('batch-proxy-input');
  var weightInput = document.getElementById('batch-proxy-weight');
  if (!textarea) return;

  var text = textarea.value.trim();
  if (!text) {
    showToast('请输入代理地址', 'error');
    return;
  }

  var weight = parseInt(weightInput && weightInput.value, 10) || 50;
  if (weight < 1) weight = 1;
  if (weight > 100) weight = 100;

  var lines = text.split('\n');
  var urls = [];
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i].trim();
    if (line && !line.startsWith('#') && !line.startsWith('//')) {
      urls.push(line);
    }
  }

  if (urls.length === 0) {
    showToast('没有有效的代理地址', 'error');
    return;
  }

  showToast('正在添加 ' + urls.length + ' 个代理...');

  try {
    var result = await window.go.main.App.BatchAddProxyEntries(urls, weight);
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }

    var msg = '批量添加完成！成功 ' + (result.success || 0) + ' 个';
    if (result.skipped > 0) msg += '，跳过 ' + result.skipped + ' 个';
    if (result.failed > 0) msg += '，失败 ' + result.failed + ' 个';
    if (result.errors && result.errors.length) {
      msg += '：' + result.errors[0];
    }
    showToast(msg, result.success > 0 ? 'success' : 'error');

    closeBatchAddProxyModal();
    await loadProxyPool();
  } catch (e) {
    showToast('批量添加失败: ' + e.message, 'error');
  }
}

function removePendingProxyRow(idx) {
  pendingEmptyRows = Math.max(0, pendingEmptyRows - 1);
  // 至少保留一个空行（如果完全没有已保存代理）
  if (proxyPool.length === 0 && pendingEmptyRows === 0) pendingEmptyRows = 1;
  renderProxyPool();
}

async function savePendingProxyRow(idx, rawURL) {
  var url = (rawURL || '').trim();
  if (!url) {
    // 留空不持久化；什么也不做
    return;
  }
  // 从 DOM 拿当前权重
  var box = document.getElementById('proxy-pool-list');
  var weight = 1;
  if (box) {
    var wEl = box.querySelector('[data-pending-weight="' + idx + '"]');
    if (wEl) {
      var w = parseInt(wEl.value, 10);
      if (!isNaN(w) && w >= 1) weight = Math.min(100, w);
    }
  }
  try {
    var res = await window.go.main.App.AddProxyEntry('', url, weight);
    if (res && res.error) {
      showToast(res.error, 'error');
      return;
    }
    pendingEmptyRows = Math.max(0, pendingEmptyRows - 1);
    showToast('已保存');
    await loadProxyPool();
  } catch (e) {
    showToast('保存失败: ' + e.message, 'error');
  }
}

async function updateProxyEntryURL(id, newURL) {
  var entry = proxyPool.find(function(p) { return p.id === id; });
  if (!entry) return;
  var url = (newURL || '').trim();
  if (url === '') {
    // 用户清空了 URL → 删除该条
    await deleteProxyEntry(id, true);
    return;
  }
  if (url === entry.url) return;
  try {
    var res = await window.go.main.App.UpdateProxyEntry(id, '', url, entry.weight || 1, entry.enabled);
    if (res && res.error) {
      showToast(res.error, 'error');
      await loadProxyPool();
      return;
    }
    await loadProxyPool();
  } catch (e) {
    showToast('更新失败: ' + e.message, 'error');
    await loadProxyPool();
  }
}

async function updateProxyEntry(id, field, value) {
  var entry = proxyPool.find(function(p) { return p.id === id; });
  if (!entry) return;
  if (field === 'weight') {
    var w = parseInt(value, 10) || 1;
    if (w < 1) w = 1;
    if (w > 100) w = 100;
    entry.weight = w;
  } else if (field === 'enabled') {
    entry.enabled = !!value;
  }
  try {
    var res = await window.go.main.App.UpdateProxyEntry(id, '', entry.url || '', entry.weight || 1, entry.enabled);
    if (res && res.error) {
      showToast(res.error, 'error');
      await loadProxyPool();
      return;
    }
    renderProxyPool();
  } catch (e) {
    showToast('更新失败: ' + e.message, 'error');
    await loadProxyPool();
  }
}

async function deleteProxyEntry(id, silent) {
  if (silent) {
    try {
      await window.go.main.App.DeleteProxyEntry(id);
    } catch (e) {}
    await loadProxyPool();
    return;
  }
  showConfirmModal('删除代理', '确认从池中删除该代理？', '确认删除', async function() {
    try {
      var res = await window.go.main.App.DeleteProxyEntry(id);
      if (res && res.error) {
        showToast(res.error, 'error');
        return;
      }
      showToast('已删除');
      await loadProxyPool();
    } catch (e) {
      showToast('删除失败: ' + e.message, 'error');
    }
  });
}

async function testProxyEntryByIdx(idx) {
  var p = proxyPool[idx];
  if (!p || !p.url) return;
  showToast('正在测试…');
  var info = await runProxyTest(p);
  if (info && info.ok) {
    var loc = [info.country, info.region, info.city].filter(Boolean).join(' · ');
    showToast((info.scheme || '').toUpperCase() + ' · ' + (info.ip || '') + (loc ? ' (' + loc + ')' : ''));
  } else {
    showToast('不可用: ' + ((info && info.error) || '未知错误'), 'error');
  }
}

function getSelectedProxyIds() {
  var ids = [];
  for (var i = 0; i < proxyPool.length; i++) {
    if (selectedProxyIds[proxyPool[i].id]) ids.push(proxyPool[i].id);
  }
  return ids;
}

function getSelectedProxyEntries() {
  var out = [];
  for (var i = 0; i < proxyPool.length; i++) {
    if (selectedProxyIds[proxyPool[i].id]) out.push(proxyPool[i]);
  }
  return out;
}

function syncProxySelectAllUI() {
  var n = getSelectedProxyIds().length;
  var countEl = document.getElementById('proxy-selected-count');
  if (countEl) countEl.textContent = '已选 ' + n;
  var allEl = document.getElementById('proxy-select-all');
  if (allEl) {
    allEl.checked = proxyPool.length > 0 && n === proxyPool.length;
    allEl.indeterminate = n > 0 && n < proxyPool.length;
  }
}

function toggleProxyRow(id, checked) {
  if (checked) selectedProxyIds[id] = true;
  else delete selectedProxyIds[id];
  syncProxySelectAllUI();
}

function toggleProxySelectAll(checked) {
  selectedProxyIds = {};
  if (checked) {
    for (var i = 0; i < proxyPool.length; i++) selectedProxyIds[proxyPool[i].id] = true;
  }
  var chks = document.querySelectorAll('.proxy-row-chk');
  for (var j = 0; j < chks.length; j++) chks[j].checked = !!checked;
  syncProxySelectAllUI();
}

function applyProxyTestStatus(p, info) {
  if (!p || !p.id) return;
  if (info && info.ok) {
    var loc = [info.country, info.city].filter(Boolean).join(' ');
    proxyTestStatus[p.id] = { ok: true, text: loc || '可用' };
  } else {
    proxyTestStatus[p.id] = { ok: false, text: '失败' };
  }
}

async function runProxyTest(p) {
  proxyTestStatus[p.id] = { pending: true, text: '测试中' };
  renderProxyPool();
  try {
    var info = await window.go.main.App.TestProxyEntry(p.url);
    applyProxyTestStatus(p, info);
    renderProxyPool();
    return info || { ok: false, error: '无结果' };
  } catch (e) {
    applyProxyTestStatus(p, { ok: false, error: e.message });
    renderProxyPool();
    return { ok: false, error: e.message };
  }
}

function runWithConcurrency(items, limit, worker) {
  var i = 0;
  var running = 0;
  return new Promise(function(resolve) {
    if (!items.length) { resolve(); return; }
    function next() {
      if (i >= items.length && running === 0) { resolve(); return; }
      while (running < limit && i < items.length) {
        running++;
        worker(items[i++]).then(function() {
          running--;
          next();
        }, function() {
          running--;
          next();
        });
      }
    }
    next();
  });
}

async function batchTestSelectedProxies() {
  var list = getSelectedProxyEntries().filter(function(p) { return p && p.url; });
  if (!list.length) {
    showToast('请先勾选要测试的代理', 'error');
    return;
  }
  if (proxyBatchTesting) {
    showToast('批量测试进行中…');
    return;
  }
  proxyBatchTesting = true;
  showToast('正在测试 ' + list.length + ' 个代理…');
  var ok = 0, fail = 0;
  try {
    await runWithConcurrency(list, 3, async function(p) {
      var info = await runProxyTest(p);
      if (info && info.ok) ok++;
      else fail++;
    });
    showToast('测试完成：可用 ' + ok + ' / 失败 ' + fail, fail ? 'error' : undefined);
  } finally {
    proxyBatchTesting = false;
  }
}

function batchCopySelectedProxies() {
  var list = getSelectedProxyEntries();
  if (!list.length) {
    showToast('请先勾选要复制的代理', 'error');
    return;
  }
  var text = list.map(function(p) { return p.url || ''; }).filter(Boolean).join('\n');
  if (!text) {
    showToast('选中项没有可复制的地址', 'error');
    return;
  }
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(function() {
      showToast('已复制 ' + list.length + ' 条');
    }).catch(function() {
      fallbackCopyProxyText(text, list.length);
    });
  } else {
    fallbackCopyProxyText(text, list.length);
  }
}

function fallbackCopyProxyText(text, n) {
  var ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.left = '-9999px';
  document.body.appendChild(ta);
  ta.select();
  try {
    document.execCommand('copy');
    showToast('已复制 ' + n + ' 条');
  } catch (e) {
    showToast('复制失败', 'error');
  }
  document.body.removeChild(ta);
}

function batchDeleteSelectedProxies() {
  var ids = getSelectedProxyIds();
  if (!ids.length) {
    showToast('请先勾选要删除的代理', 'error');
    return;
  }
  showConfirmModal('批量删除', '确认删除选中的 ' + ids.length + ' 条代理？', '确认删除', async function() {
    try {
      var res = await window.go.main.App.BatchDeleteProxyEntries(ids);
      if (res && res.error) {
        showToast(res.error, 'error');
        return;
      }
      selectedProxyIds = {};
      showToast('已删除 ' + (res && res.deleted != null ? res.deleted : ids.length) + ' 条');
      await loadProxyPool();
    } catch (e) {
      showToast('批量删除失败: ' + e.message, 'error');
    }
  });
}

async function batchSetSelectedProxyWeight() {
  var ids = getSelectedProxyIds();
  if (!ids.length) {
    showToast('请先勾选要改权重的代理', 'error');
    return;
  }
  var el = document.getElementById('proxy-batch-weight');
  var w = el ? parseInt(el.value, 10) : 50;
  if (isNaN(w) || w < 1) w = 1;
  if (w > 100) w = 100;
  try {
    var res = await window.go.main.App.BatchSetProxyWeight(ids, w);
    if (res && res.error) {
      showToast(res.error, 'error');
      return;
    }
    showToast('已将 ' + (res && res.updated != null ? res.updated : ids.length) + ' 条权重设为 ' + w);
    await loadProxyPool();
  } catch (e) {
    showToast('设置权重失败: ' + e.message, 'error');
  }
}
