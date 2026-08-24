// ===== UI工具：Toast / 窗口控制 / 主题 / 健康检查 / 邮箱提供商 =====

// Toast 通知
function showToast(msg, type) {
  // 容器
  var container = document.getElementById('toast-container');
  if (!container) {
    container = document.createElement('div');
    container.id = 'toast-container';
    document.body.appendChild(container);
  }

  var toast = document.createElement('div');
  toast.className = 'toast-item' + (type === 'error' ? ' toast-error' : ' toast-success');

  // 图标
  var icon = type === 'error'
    ? '<svg viewBox="0 0 24 24" class="toast-icon"><circle cx="12" cy="12" r="10"/><path d="M15 9l-6 6M9 9l6 6"/></svg>'
    : '<svg viewBox="0 0 24 24" class="toast-icon"><circle cx="12" cy="12" r="10"/><path d="M9 12l2 2 4-4"/></svg>';

  toast.innerHTML = icon + '<span class="toast-msg">' + msg + '</span>' +
    '<div class="toast-progress"><div class="toast-progress-bar"></div></div>';

  container.appendChild(toast);

  // 触发入场动画
  requestAnimationFrame(function() { toast.classList.add('show'); });

  // 自动消失
  setTimeout(function() {
    toast.classList.remove('show');
    toast.classList.add('hide');
    setTimeout(function() { toast.remove(); }, 400);
  }, 3000);
}

// 窗口控制
function closeApp() {
  try {
    if (window.runtime && window.runtime.Quit) { window.runtime.Quit(); }
    else { window.close(); }
  } catch (e) { console.error('关闭窗口失败:', e); }
}

function minimizeApp() {
  try {
    if (window.runtime && window.runtime.WindowMinimise) { window.runtime.WindowMinimise(); }
  } catch (e) { console.error('最小化窗口失败:', e); }
}

function maximizeApp() {
  try {
    if (window.runtime && window.runtime.WindowToggleMaximise) { window.runtime.WindowToggleMaximise(); }
  } catch (e) { console.error('最大化窗口失败:', e); }
}

// 快捷键
document.addEventListener('keydown', function(e) {
  // Ctrl+Enter 开始任务
  if (e.ctrlKey && e.key === 'Enter') {
    e.preventDefault();
    if (!document.getElementById('btn-start').disabled) startTask();
  }
  // Esc 停止任务
  if (e.key === 'Escape') {
    if (!document.getElementById('btn-stop').disabled) stopTask();
  }
});

// 当前选中的邮箱提供商
var selectedEmailProvider = 'outlook';
var selectedCloudMailDomains = [];
var allCloudMailDomains = []; // 存储所有 cloud-mail 域名及对应配置
var domainPoolStatus = {};   // 域名池状态覆盖（禁用/拉黑域名置灰）

// HTML 转义函数
function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// 初始化邮箱提供商选择（页面加载时调用）
function initEmailProviderSelection() {
  // 默认选中 Outlook
  selectEmailProvider('outlook');
}

// 选择邮箱提供商
function selectEmailProvider(provider) {
  selectedEmailProvider = provider;

  // 更新按钮样式
  const outlookBtn = document.querySelector('label[onclick*="outlook"]');
  const cloudmailBtn = document.querySelector('label[onclick*="cloudmail"]');

  // 全部还原
  [outlookBtn, cloudmailBtn].forEach(b => {
    if (b) { b.style.borderColor = 'var(--border)'; b.style.background = 'transparent'; }
  });

  let activeBtn = outlookBtn;
  if (provider === 'cloudmail') activeBtn = cloudmailBtn;
  if (activeBtn) {
    activeBtn.style.borderColor = 'var(--primary)';
    activeBtn.style.background = 'rgba(59, 130, 246, 0.1)';
  }

  // 显示/隐藏配置块
  const cloudmailSel = document.getElementById('cloudmail-sel');
  const hintEl = document.getElementById('ep-hint');

  if (cloudmailSel) cloudmailSel.style.display = (provider === 'cloudmail') ? 'block' : 'none';

  // 更新 pcard 选中状态
  document.querySelectorAll('.pcard').forEach(function(c) { c.classList.remove('sel'); });
  var activeCard = document.getElementById('pcard-' + provider);
  if (activeCard) activeCard.classList.add('sel');

  if (provider === 'cloudmail') {
    if (hintEl) { hintEl.textContent = _uiT('register.cloudmailHint', '使用 Cloud-Mail 自部署邮箱注册。⚠️ 每次注册会创建永久账号，需手动清理。'); }
    loadCloudMailDomainsToList();
  } else if (provider === 'httpapi') {
    if (hintEl) { hintEl.textContent = _uiT('register.httpapiHint', '使用 HTTP API 邮箱注册。卡密：邮箱----API地址（支持「邮箱：」前缀）'); }
  } else {
    if (hintEl) { hintEl.textContent = _uiT('register.outlookHintFull', '使用 Outlook 账号进行注册，代理配置请在设置页设置。'); }
  }
}

function _uiT(key, fallback) {
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key);
    if (v && v !== key) return v;
  }
  return fallback;
}

// ===== Cloud-Mail 域名加载/选择 =====
async function loadCloudMailDomainsToList() {
  const listDiv = document.getElementById('cfg-cloudmail-domains-list');
  if (!listDiv) return;

  listDiv.innerHTML = '<div style="text-align:center;color:var(--text-muted);font-size:12px;padding:12px;">' + _uiT('common.loading', '加载中...') + '</div>';

  try {
    const configs = await window.go.main.App.GetCloudMailConfigs();
    if (!configs || configs.length === 0) {
      listDiv.innerHTML = '<div style="text-align:center;color:var(--text-muted);font-size:12px;padding:12px;">' + _uiT('cloudmail.noDomainsHint', '暂无配置，请先在邮箱池页添加') + '</div>';
      return;
    }

    let configStatus = {};
    try {
      const saved = localStorage.getItem('cloudmail-config-status');
      if (saved) configStatus = JSON.parse(saved);
    } catch (e) {}

    allCloudMailDomains = [];
    const domainConfigMap = {};

    for (const cfg of configs) {
      const status = configStatus[cfg.name];
      // 测试通过优先用服务器返回的域名；否则（status 丢失/未测试）回退配置里保存的域名
      let domains = (cfg.domains || []).slice();
      if (status && status.tested && status.success && status.domains && status.domains.length > 0) {
        domains = status.domains;
      }
      for (const domain of domains) {
        if (!domainConfigMap[domain]) domainConfigMap[domain] = [];
        domainConfigMap[domain].push(cfg);
      }
    }

    allCloudMailDomains = Object.keys(domainConfigMap).map(domain => ({
      domain: domain,
      configs: domainConfigMap[domain]
    }));

    // 域名池状态（禁用/拉黑域名置灰不可选）
    domainPoolStatus = {};
    try {
      const dp = await window.go.main.App.ListDomainPool();
      (dp || []).forEach(d => { domainPoolStatus[d.domain] = d; });
    } catch (e) {}

    if (allCloudMailDomains.length === 0) {
      listDiv.innerHTML = '<div style="text-align:center;color:var(--text-muted);font-size:12px;padding:12px;">' + _uiT('cloudmail.noActiveDomain', '暂无可用域名，请先测试 Cloud-Mail 配置') + '</div>';
      return;
    }

    let html = `
      <div class="domain-mode-row">
        <div class="domain-mode-btn selected" data-domain="__random__" onclick="toggleCloudMailDomain('__random__')">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 3 21 3 21 8"/><line x1="4" y1="20" x2="21" y2="3"/><polyline points="21 16 21 21 16 21"/><line x1="15" y1="15" x2="21" y2="21"/><line x1="4" y1="4" x2="9" y2="9"/></svg>
          ${_uiT('register.modeRandom', '随机')}
        </div>
        <div class="domain-mode-btn" data-domain="__all__" onclick="toggleCloudMailDomain('__all__')">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="17 1 21 5 17 9"/><path d="M3 11V9a4 4 0 014-4h14"/><polyline points="7 23 3 19 7 15"/><path d="M21 13v2a4 4 0 01-4 4H3"/></svg>
          ${_uiT('register.modeRoundRobin', '轮询')}
        </div>
      </div>
      <div class="domain-chips-wrap">
    `;

    html += allCloudMailDomains.map((item) => {
      const st = domainPoolStatus[item.domain];
      const off = st && (!st.enabled || st.banned);
      const tag = st && st.banned ? ' 🔒' : (st && !st.enabled ? ' ⛔' : '');
      const tip = st && st.banned ? '已拉黑（TES 风控）' : (st && !st.enabled ? '已禁用' : item.configs.length + ' 个配置');
      return `<div class="domain-chip${off ? ' off' : ''}" data-domain="${escapeHtml(item.domain)}" onclick="toggleCloudMailDomain('${escapeHtml(item.domain)}')" title="${tip}">${escapeHtml(item.domain)}${tag}</div>`;
    }).join('');

    html += '</div>';
    listDiv.innerHTML = html;
    selectedCloudMailDomains = ['__random__'];
    updateCloudMailDomainStyles();
  } catch (e) {
    console.error('加载 Cloud-Mail 域名失败:', e);
    listDiv.innerHTML = '<div style="text-align:center;color:var(--danger);font-size:12px;padding:12px;">加载失败</div>';
  }
}

function updateCloudMailDomainStyles() {
  const container = document.getElementById('cfg-cloudmail-domains-list');
  if (!container) return;
  container.querySelectorAll('.domain-mode-btn').forEach(el => {
    const d = el.getAttribute('data-domain');
    el.classList.toggle('selected', selectedCloudMailDomains.includes(d));
  });
  container.querySelectorAll('.domain-chip').forEach(el => {
    const d = el.getAttribute('data-domain');
    el.classList.toggle('selected', !el.classList.contains('off') && selectedCloudMailDomains.includes(d));
  });
}

function toggleCloudMailDomain(domain) {
  if (domain !== '__random__' && domain !== '__all__') {
    const st = domainPoolStatus[domain];
    if (st && (!st.enabled || st.banned)) {
      if (typeof showToast === 'function') {
        showToast(st.banned ? '该域名已被 TES 拉黑，请在设置页解除拉黑' : '该域名已被禁用，请在设置页启用', 'error');
      }
      return;
    }
  }
  const isSelected = selectedCloudMailDomains.includes(domain);
  if (domain === '__random__' || domain === '__all__') {
    if (isSelected) {
      selectedCloudMailDomains = selectedCloudMailDomains.filter(d => d !== domain);
    } else {
      selectedCloudMailDomains = [domain];
    }
  } else {
    selectedCloudMailDomains = selectedCloudMailDomains.filter(d => d !== '__random__' && d !== '__all__');
    if (isSelected) {
      selectedCloudMailDomains = selectedCloudMailDomains.filter(d => d !== domain);
    } else {
      selectedCloudMailDomains.push(domain);
    }
  }
  updateCloudMailDomainStyles();
}

function selectAllCloudMailDomains() {
  selectedCloudMailDomains = allCloudMailDomains
    .filter(item => {
      const st = domainPoolStatus[item.domain];
      return !st || (st.enabled && !st.banned);
    })
    .map(item => item.domain);
  updateCloudMailDomainStyles();
}

// 关闭任务模态框
function closeKiroTaskModal() {
  var el = document.getElementById('kiro-task-modal');
  if (el) el.classList.remove('show');
}

// ===== 模态框遮罩层关闭逻辑（仅当 mousedown 和 mouseup 都在遮罩层上时才关闭） =====
(function() {
  var modalCloseMap = {
    'outlook-modal': function() { if (typeof closeOutlookModal === 'function') closeOutlookModal(); },
    'httpapi-modal': function() { if (typeof closeHttpAPIModal === 'function') closeHttpAPIModal(); }
  };

  var mouseDownTarget = null;

  Object.keys(modalCloseMap).forEach(function(id) {
    var overlay = document.getElementById(id);
    if (!overlay) return;

    overlay.addEventListener('mousedown', function(e) {
      mouseDownTarget = e.target;
    });

    overlay.addEventListener('mouseup', function(e) {
      if (mouseDownTarget === overlay && e.target === overlay) {
        modalCloseMap[id]();
      }
      mouseDownTarget = null;
    });
  });
})();

