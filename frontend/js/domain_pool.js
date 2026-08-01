// ===== 域名池（设置页，供 Cloud-Mail 注册机使用） =====
// 域名由 Cloud-Mail 配置自动发现聚合；池只管理状态：启用/禁用/自动拉黑。

var domainPool = [];

function escapeDomainHtml(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, function(c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

function _dpT(key, fallback) {
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key);
    if (v && v !== key) return v;
  }
  return fallback;
}

async function loadDomainPool() {
  var box = document.getElementById('domain-pool-list');
  if (!box) return;
  try {
    var list = await window.go.main.App.ListDomainPool();
    domainPool = list || [];
  } catch (e) {
    domainPool = [];
  }
  renderDomainPool();
}

function renderDomainPool() {
  var box = document.getElementById('domain-pool-list');
  if (!box) return;

  if (!domainPool || domainPool.length === 0) {
    box.innerHTML = '<div style="text-align:center;color:var(--text-3);font-size:12px;padding:12px;">' +
      _dpT('domainpool.empty', '暂无域名，请先在邮箱池页添加并测试 Cloud-Mail 配置') + '</div>';
    return;
  }

  var html = '<div style="display:flex;flex-direction:column;gap:6px;">';
  for (var i = 0; i < domainPool.length; i++) {
    var d = domainPool[i];
    var badge;
    if (d.banned) {
      badge = '<span class="badge badge-err" style="font-size:10px;">' + _dpT('domainpool.banned', '已拉黑') + '</span>';
    } else if (!d.enabled) {
      badge = '<span class="badge badge-idle" style="font-size:10px;">' + _dpT('domainpool.disabled', '已禁用') + '</span>';
    } else {
      badge = '<span class="badge badge-ok" style="font-size:10px;">' + _dpT('domainpool.active', '正常') + '</span>';
    }
    html += (
      '<div style="display:flex;align-items:center;gap:8px;padding:6px 10px;border:1px solid var(--border);border-radius:8px;">' +
        '<span style="flex:1;font-family:var(--font-mono);font-size:12px;overflow:hidden;text-overflow:ellipsis;">' + escapeDomainHtml(d.domain) + '</span>' +
        '<span style="font-size:10px;color:var(--text-3);">' + (d.configCount || 0) + ' ' + _dpT('domainpool.servers', '台服务器') + '</span>' +
        badge +
        (d.banned
          ? '<button type="button" class="btn btn-outline btn-sm" onclick="unbanDomain(\'' + escapeDomainHtml(d.domain) + '\')">' + _dpT('domainpool.unban', '解除拉黑') + '</button>'
          : '<button type="button" class="btn btn-outline btn-sm" onclick="toggleDomainEnabled(\'' + escapeDomainHtml(d.domain) + '\')">' +
            (d.enabled ? _dpT('domainpool.disable', '禁用') : _dpT('domainpool.enable', '启用')) + '</button>')
      + '</div>'
    );
  }
  html += '</div>';
  box.innerHTML = html;
}

async function toggleDomainEnabled(domain) {
  var item = domainPool.find(function(d) { return d.domain === domain; });
  if (!item) return;
  var enabled = !item.enabled;
  try {
    var res = await window.go.main.App.SetDomainEnabled(domain, enabled);
    if (res && res.error) { showToast(res.error, 'error'); return; }
    await loadDomainPool();
  } catch (e) {
    showToast(String(e), 'error');
  }
}

async function unbanDomain(domain) {
  try {
    var res = await window.go.main.App.UnbanDomain(domain);
    if (res && res.error) { showToast(res.error, 'error'); return; }
    showToast(_dpT('domainpool.unbanOk', '已解除拉黑，域名恢复使用'), 'success');
    await loadDomainPool();
  } catch (e) {
    showToast(String(e), 'error');
  }
}
