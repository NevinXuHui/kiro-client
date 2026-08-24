// ===== 代理池（行内编辑式） =====
// 设计：默认显示一个空输入框；URL 留空=直连。点击「+ 添加代理」追加新行；
// 失焦/回车时持久化。同一时刻可以有多个空行，但保存时空 URL 行不会落库。

var proxyPool = [];        // 来自后端的真实条目（含 id）
var pendingEmptyRows = 1;  // 还未保存的空行数（最少 1 个）

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
    html += (
      '<div style="display:flex;align-items:center;gap:6px;margin-bottom:6px;">' +
        '<input type="text" value="' + escapeProxyHtml(p.url) + '" placeholder="留空=直连" onchange="updateProxyEntryURL(\'' + p.id + '\', this.value)" class="form-input" style="flex:1;font-family:var(--font-mono);font-size:12px;">' +
        '<input type="number" min="1" max="100" value="' + (p.weight || 1) + '" title="权重 1-100" onchange="updateProxyEntry(\'' + p.id + '\', \'weight\', this.value)" style="width:54px;text-align:center;padding:4px;border:1px solid var(--border);border-radius:4px;background:var(--bg-subtle);font-size:12px;">' +
        (pct != null ? '<span style="font-size:11px;color:var(--text-muted);min-width:42px;text-align:right;">' + pct + '%</span>' : '') +
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

async function showBatchAddProxyModal() {
  var content = '<div>' +
    '<p style="margin-bottom:12px;color:var(--text-muted);font-size:13px;">每行一个代理地址，支持以下格式：</p>' +
    '<ul style="margin:0 0 12px 20px;padding:0;list-style:disc;color:var(--text-muted);font-size:12px;">' +
    '<li>http://host:port</li>' +
    '<li>http://user:pass@host:port</li>' +
    '<li>socks5://host:port</li>' +
    '<li>socks5://user:pass@host:port</li>' +
    '<li>空行和 # 开头的注释行会被跳过</li>' +
    '</ul>' +
    '<textarea id="batch-proxy-input" rows="10" placeholder="http://proxy1.example.com:8080\nhttp://user:pass@proxy2.example.com:8080\nsocks5://proxy3.example.com:1080\n# 这是注释" style="width:100%;padding:8px;border:1px solid var(--border);border-radius:4px;background:var(--bg-subtle);font-family:var(--font-mono);font-size:12px;resize:vertical;"></textarea>' +
    '<div style="margin-top:12px;display:flex;align-items:center;gap:8px;">' +
    '<label style="font-size:13px;color:var(--text-2);">默认权重:</label>' +
    '<input type="number" id="batch-proxy-weight" min="1" max="100" value="50" style="width:80px;padding:4px 8px;border:1px solid var(--border);border-radius:4px;background:var(--bg-subtle);text-align:center;">' +
    '<span style="font-size:12px;color:var(--text-muted);">（1-100，推荐 50）</span>' +
    '</div>' +
    '</div>';

  showConfirmModal('批量添加代理', content, '添加', async function() {
    var textarea = document.getElementById('batch-proxy-input');
    var weightInput = document.getElementById('batch-proxy-weight');
    if (!textarea) return;

    var text = textarea.value.trim();
    if (!text) {
      showToast('请输入代理地址', 'error');
      return;
    }

    var weight = parseInt(weightInput.value, 10) || 50;
    if (weight < 1) weight = 1;
    if (weight > 100) weight = 100;

    // 按行分割
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

      // 显示结果
      var msg = '批量添加完成！\n';
      msg += '成功: ' + (result.success || 0) + ' 个\n';
      if (result.skipped > 0) msg += '跳过（重复）: ' + result.skipped + ' 个\n';
      if (result.failed > 0) msg += '失败: ' + result.failed + ' 个\n';
      if (result.errors && result.errors.length > 0) {
        msg += '\n错误信息:\n' + result.errors.join('\n');
      }

      showToast(msg.replace(/\n/g, '<br>'), result.success > 0 ? 'success' : 'error');

      // 刷新列表
      await loadProxyPool();
    } catch (e) {
      showToast('批量添加失败: ' + e.message, 'error');
    }
  });
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
  try {
    var info = await window.go.main.App.TestProxyEntry(p.url);
    if (info && info.ok) {
      var loc = [info.country, info.region, info.city].filter(Boolean).join(' · ');
      showToast((info.scheme || '').toUpperCase() + ' · ' + (info.ip || '') + (loc ? ' (' + loc + ')' : ''));
    } else {
      showToast('不可用: ' + ((info && info.error) || '未知错误'), 'error');
    }
  } catch (e) {
    showToast('测试失败: ' + e.message, 'error');
  }
}
