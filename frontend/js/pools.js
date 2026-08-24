// ===== 号池管理 - Dashboard =====

let currentPool = null;
let selectedAccountIdx = -1;
let usageChart = null;
let healthChart = null;
let modelsChart = null;
let poolAutoRefreshTimer = null; // 自动刷新定时器

// 初始化：页面切换时加载
window.addEventListener('DOMContentLoaded', function() {
  const observer = new MutationObserver(function(mutations) {
    mutations.forEach(function(mutation) {
      if (mutation.attributeName === 'class') {
        const poolsPage = document.getElementById('page-pools');
        if (poolsPage && poolsPage.classList.contains('active')) {
          loadPools();
          startAutoRefresh(); // 页面可见时启动自动刷新
        } else {
          stopAutoRefresh(); // 页面隐藏时停止
        }
      }
    });
  });

  const poolsPage = document.getElementById('page-pools');
  if (poolsPage) {
    observer.observe(poolsPage, { attributes: true });
  }
});

// 自动刷新：每 30s 刷新号池数据（9router 同款：dashboard 定时轮询）
function startAutoRefresh() {
  stopAutoRefresh();
  poolAutoRefreshTimer = setInterval(async function() {
    if (!currentPool) return;
    try {
      const result = await window.go.main.App.GetPool(currentPool.id);
      if (result && !result.error) {
        // 保留用户选中状态
        const oldIdx = selectedAccountIdx;
        const oldEmail = oldIdx >= 0 && currentPool.accounts ? currentPool.accounts[oldIdx]?.email : null;
        currentPool = result;
        // 恢复选中
        if (oldEmail && currentPool.accounts) {
          selectedAccountIdx = currentPool.accounts.findIndex(a => a.email === oldEmail);
        }
        renderDashboard();
      }
    } catch (err) {
      // 静默失败，不弹 Toast
    }
  }, 30000);
}

function stopAutoRefresh() {
  if (poolAutoRefreshTimer) {
    clearInterval(poolAutoRefreshTimer);
    poolAutoRefreshTimer = null;
  }
}

function _plT(key, fallback) {
  if (window.I18N && typeof window.I18N.t === 'function') {
    var v = window.I18N.t(key);
    if (v && v !== key) return v;
  }
  return fallback;
}

var _PL_ICON_OK = '<svg viewBox="0 0 24 24"><rect x="1.2" y="8.2" width="21.6" height="7.6" rx="3.8" transform="rotate(-45 12 12)"/><path d="m8.4 8.4 7.2 7.2"/></svg>';
var _PL_ICON_BAD = '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m9 9 6 6M15 9l-6 6"/></svg>';
var _PL_ICON_DEL = '<svg viewBox="0 0 24 24"><path d="M4 7h16M9.5 7V4.6h5V7M6.5 7l.8 12.4a1.6 1.6 0 0 0 1.6 1.5h6.2a1.6 1.6 0 0 0 1.6-1.5L17.5 7"/></svg>';

// 加载号池
async function loadPools() {
  try {
    const pools = await window.go.main.App.ListPools() || [];
    // 默认展示「默认号池」；不存在则选账号最多的池（避免停留在空/小池上）
    let chosen = null;
    for (const p of pools) {
      const n = p.accounts ? p.accounts.length : 0;
      if (!chosen || p.name === '默认号池' || (chosen.name !== '默认号池' && n > (chosen.accounts ? chosen.accounts.length : 0))) {
        chosen = p;
      }
    }
    currentPool = chosen;
    poolPage = 1;
    renderDashboard();
  } catch (err) {
    console.error('Failed to load pools:', err);
    renderDashboard();
  }
}

// ===== 主渲染入口 =====
function renderDashboard() {
  renderStatsCards();
  renderUsageChart();
  renderHealthChart();
  renderModelsChart();
  renderAccountsList();
  renderQuotaOverview();
  renderActivityFeed();
  // re-init lucide icons in the pool page
  if (window.lucide) lucide.createIcons();
}

// ===== 统计卡片 =====
function renderStatsCards() {
  const accounts = (currentPool && currentPool.accounts) ? currentPool.accounts : [];
  const total = accounts.length;
  const healthy = accounts.filter(a => a.healthStatus === 'healthy').length;
  const unhealthy = accounts.filter(a => a.healthStatus === 'unhealthy').length;

  // collect unique models
  const modelSet = new Set();
  accounts.forEach(a => {
    if (a.availableModels) a.availableModels.forEach(m => modelSet.add(m));
  });

  document.getElementById('pool-total-count').textContent = total;
  document.getElementById('pool-healthy-count').textContent = healthy;
  document.getElementById('pool-unhealthy-count').textContent = unhealthy;
  document.getElementById('pool-models-count').textContent = modelSet.size;
}

// ===== 折线图：账号使用趋势 =====
function renderUsageChart() {
  const canvas = document.getElementById('pool-usage-chart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const accounts = (currentPool && currentPool.accounts) ? currentPool.accounts : [];

  const labels = accounts.map(a => (a.email || 'Unknown').split('@')[0]);
  const usedData = accounts.map(a => {
    if (a.usageQuotas && a.usageQuotas.quotas) {
      const q = a.usageQuotas.quotas;
      const keys = Object.keys(q);
      if (keys.length > 0) return q[keys[0]].used || 0;
    }
    return a.creditUsed || 0;
  });
  const totalData = accounts.map(a => {
    if (a.usageQuotas && a.usageQuotas.quotas) {
      const q = a.usageQuotas.quotas;
      const keys = Object.keys(q);
      if (keys.length > 0) return q[keys[0]].total || 0;
    }
    return a.creditLimit || 0;
  });

  if (usageChart) usageChart.destroy();

  const gradient = ctx.createLinearGradient(0, 0, 0, 200);
  gradient.addColorStop(0, 'rgba(47,128,245,0.25)');
  gradient.addColorStop(1, 'rgba(47,128,245,0.02)');

  usageChart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels.length > 0 ? labels : ['--'],
      datasets: [
        {
          label: _plT('pools.used', '已使用'),
          data: usedData.length > 0 ? usedData : [0],
          borderColor: '#2f80f5',
          backgroundColor: gradient,
          fill: true,
          tension: 0.4,
          borderWidth: 2,
          pointRadius: 3,
          pointBackgroundColor: '#2f80f5',
          pointBorderColor: '#fff',
          pointBorderWidth: 2,
        },
        {
          label: _plT('pools.total', '总额度'),
          data: totalData.length > 0 ? totalData : [0],
          borderColor: '#e0e7f2',
          backgroundColor: 'transparent',
          borderWidth: 2,
          borderDash: [5, 5],
          pointRadius: 0,
          tension: 0.4,
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          position: 'top',
          align: 'end',
          labels: { boxWidth: 12, boxHeight: 2, padding: 16, font: { size: 11 }, color: '#6b7891' }
        }
      },
      scales: {
        x: {
          grid: { display: false },
          ticks: { font: { size: 10 }, color: '#9aa5b8', maxRotation: 45 }
        },
        y: {
          grid: { color: 'rgba(238,241,247,0.6)' },
          ticks: { font: { size: 10 }, color: '#9aa5b8' },
          beginAtZero: true
        }
      },
      interaction: { intersect: false, mode: 'index' }
    }
  });
}

// ===== 环形图：健康分布 =====
function renderHealthChart() {
  const canvas = document.getElementById('pool-health-chart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const accounts = (currentPool && currentPool.accounts) ? currentPool.accounts : [];

  const healthy = accounts.filter(a => a.healthStatus === 'healthy').length;
  const unhealthy = accounts.filter(a => a.healthStatus === 'unhealthy').length;
  const unknown = accounts.filter(a => !a.healthStatus || a.healthStatus === 'unknown').length;

  if (healthChart) healthChart.destroy();

  healthChart = new Chart(ctx, {
    type: 'doughnut',
    data: {
      labels: [_plT('pools.healthy', '健康'), _plT('pools.unhealthy', '异常'), _plT('pools.unknown', '未知')],
      datasets: [{
        data: [healthy || 0, unhealthy || 0, unknown || 0],
        backgroundColor: ['#43c97c', '#f0564d', '#c5cdd8'],
        borderWidth: 0,
        hoverOffset: 4
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      cutout: '65%',
      plugins: {
        legend: {
          position: 'bottom',
          labels: { boxWidth: 10, boxHeight: 10, padding: 12, font: { size: 11 }, color: '#6b7891' }
        }
      }
    }
  });
}

// ===== 柱状图：模型分布 =====
function renderModelsChart() {
  const canvas = document.getElementById('pool-models-chart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const accounts = (currentPool && currentPool.accounts) ? currentPool.accounts : [];

  const modelCount = {};
  accounts.forEach(a => {
    if (a.availableModels) {
      a.availableModels.forEach(m => {
        modelCount[m] = (modelCount[m] || 0) + 1;
      });
    }
  });

  const sorted = Object.entries(modelCount).sort((a, b) => b[1] - a[1]).slice(0, 8);
  const labels = sorted.map(e => e[0]);
  const data = sorted.map(e => e[1]);

  if (modelsChart) modelsChart.destroy();

  modelsChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels.length > 0 ? labels : ['--'],
      datasets: [{
        label: _plT('pools.accountCount', '账号数'),
        data: data.length > 0 ? data : [0],
        backgroundColor: 'rgba(47,128,245,0.75)',
        borderRadius: 6,
        barThickness: 18,
        maxBarThickness: 18
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      indexAxis: 'y',
      layout: { padding: { right: 10 } },
      plugins: {
        legend: { display: false }
      },
      scales: {
        x: {
          grid: { color: 'rgba(238,241,247,0.6)' },
          ticks: { font: { size: 10 }, color: '#9aa5b8', stepSize: 1 },
          beginAtZero: true,
          suggestedMax: Math.max(...(data.length > 0 ? data : [1])) + 1
        },
        y: {
          grid: { display: false },
          ticks: { font: { size: 10 }, color: '#6b7891' }
        }
      }
    }
  });
}

// ===== 账号列表 =====
const POOL_PAGE_SIZE = 50; // 分页渲染，万级账号不卡 DOM
let poolPage = 1;

function renderAccountsList() {
  const container = document.getElementById('pools-list');
  if (!container) return;

  if (!currentPool || !currentPool.accounts || currentPool.accounts.length === 0) {
    container.innerHTML = `
      <div class="empty">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 8v9.4a1.6 1.6 0 0 1-1.6 1.6H4.6A1.6 1.6 0 0 1 3 17.4V8"/>
          <rect x="2" y="4.4" width="20" height="4.2" rx="1.4"/>
          <path d="M9.6 12.4h4.8"/>
        </svg>
        <p>${_plT('pools.empty', '暂无账号')}</p>
        <p class="hint">${_plT('pools.emptyHint', '点击右上角"添加账号"导入 JSON 文件')}</p>
      </div>
    `;
    return;
  }

  const total = currentPool.accounts.length;
  const pages = Math.max(1, Math.ceil(total / POOL_PAGE_SIZE));
  if (poolPage > pages) poolPage = pages;
  const start = (poolPage - 1) * POOL_PAGE_SIZE;
  const slice = currentPool.accounts.slice(start, start + POOL_PAGE_SIZE);

  const pager = pages > 1
    ? `<div class="pool-pager" style="display:flex;align-items:center;gap:10px;justify-content:center;margin-top:12px;">
        <button class="btn btn-outline btn-sm" onclick="poolPageMove(-1)" ${poolPage <= 1 ? 'disabled' : ''}>上一页</button>
        <span style="font-size:12px;color:var(--text-3);">第 ${poolPage}/${pages} 页 · 共 ${total} 个</span>
        <button class="btn btn-outline btn-sm" onclick="poolPageMove(1)" ${poolPage >= pages ? 'disabled' : ''}>下一页</button>
      </div>`
    : '';

  container.innerHTML =
    (currentPool.name ? `<div class="hint" style="margin:0 0 8px;">${_plT('pools.current', '当前号池')}：${escapeHtml(currentPool.name)}（${total}）</div>` : '') +
    slice.map((acc, i) => renderAccountItem(acc, start + i)).join('') +
    pager;
}

function poolPageMove(delta) {
  poolPage += delta;
  renderAccountsList();
}

function renderAccountItem(acc, idx) {
  const isValid = acc.refreshToken && acc.refreshToken.startsWith('aorAAAAAG');
  const email = escapeHtml(acc.email || 'Unknown');
  const meta = [acc.subscription || 'N/A', acc.provider || 'Unknown', acc.region || 'us-east-1']
    .map(escapeHtml).join(' · ');

  const healthStatus = acc.healthStatus || 'unknown';
  const healthCode = acc.healthCode || '';
  // 9router 同款细分：429=限流(软失败,账号仍可用) / AUTH=令牌失效 / 502/503/504=上游不可用 / NET=网络
  const healthBadge = healthStatus === 'healthy' && healthCode === '429'
    ? '<span class="badge badge-warn" style="font-size:10px;" title="' + escapeHtml(acc.healthError || '') + '">429 限流</span>'
    : healthStatus === 'healthy'
    ? '<span class="badge badge-ok" style="font-size:10px;">健康</span>'
    : healthStatus === 'unhealthy'
    ? '<span class="badge badge-err" style="font-size:10px;" title="' + escapeHtml(acc.healthError || '') + '">'
      + ({AUTH:'鉴权失效', NET:'网络错误', UNKNOWN:'异常'}[healthCode] || (healthCode ? 'HTTP '+healthCode : '异常')) + '</span>'
    : '';

  let quotaInfo = '';
  if (acc.usageQuotas && acc.usageQuotas.quotas) {
    const quotas = acc.usageQuotas.quotas;
    const resourceTypes = Object.keys(quotas);
    if (resourceTypes.length > 0) {
      const firstQuota = quotas[resourceTypes[0]];
      const remaining = firstQuota.remaining || 0;
      const total = firstQuota.total || 0;
      const pct = total > 0 ? Math.min(100, (remaining / total) * 100) : 100;
      const barColor = pct <= 20 ? '#f0564d' : pct <= 50 ? '#f0b752' : '#43c97c';
      const barWidth = Math.max(4, pct);
      quotaInfo = `<span style="font-size:10px;color:var(--text-3);display:flex;align-items:center;gap:4px;">
        额度: ${remaining.toFixed(0)}/${total.toFixed(0)}
        <span style="display:inline-block;width:40px;height:4px;background:#e0e7f2;border-radius:2px;overflow:hidden;">
          <span style="display:block;height:100%;width:${barWidth}%;background:${barColor};border-radius:2px;"></span>
        </span>
      </span>`;
    }
  } else if (acc.creditLimit > 0) {
    // fallback: 从 CreditUsed/CreditLimit 字段（9router 同款）
    const remaining = Math.max(0, acc.creditLimit - acc.creditUsed);
    const pct = (remaining / acc.creditLimit) * 100;
    const barColor = pct <= 20 ? '#f0564d' : pct <= 50 ? '#f0b752' : '#43c97c';
    const barWidth = Math.max(4, pct);
    quotaInfo = `<span style="font-size:10px;color:var(--text-3);display:flex;align-items:center;gap:4px;">
      额度: ${remaining.toFixed(0)}/${acc.creditLimit.toFixed(0)}
      <span style="display:inline-block;width:40px;height:4px;background:#e0e7f2;border-radius:2px;overflow:hidden;">
        <span style="display:block;height:100%;width:${barWidth}%;background:${barColor};border-radius:2px;"></span>
      </span>
    </span>`;
  }

  let modelsInfo = '';
  if (acc.availableModels && acc.availableModels.length > 0) {
    modelsInfo = `<span style="font-size:10px;color:var(--text-3);">模型: ${acc.availableModels.length} 个</span>`;
  }

  let refreshInfo = '';
  if (acc.lastUpdatedAt) {
    const updateTime = new Date(acc.lastUpdatedAt);
    const now = new Date();
    const diffMinutes = Math.floor((now - updateTime) / 1000 / 60);
    refreshInfo = `<span style="font-size:10px;color:var(--text-3);">${diffMinutes < 1 ? '刚刚' : diffMinutes + '分钟前'}</span>`;
  }

  const selected = idx === selectedAccountIdx ? ' selected' : '';

  return `
    <div class="pa${isValid ? '' : ' bad'}${selected}" onclick="selectAccount(${idx})">
      <span class="pa-tile">${isValid ? _PL_ICON_OK : _PL_ICON_BAD}</span>
      <span class="pa-info">
        <span class="pa-email" title="${email}">${email}</span>
        <span class="pa-meta">${meta}</span>
        <span class="pa-meta" style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;">
          ${healthBadge}
          ${quotaInfo}
          ${modelsInfo}
          ${refreshInfo}
        </span>
      </span>
      <span class="pa-acts">
        ${isValid ? '' : `<span class="badge badge-err">Invalid</span>`}
        <button class="btn btn-outline btn-sm" style="min-width:auto;padding:4px 8px;font-size:11px;" onclick="event.stopPropagation();refreshSingleAccount(${idx})" title="刷新">
          <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2"/>
          </svg>
        </button>
        <button class="pa-del" title="${_plT('common.delete', '删除')}" onclick="event.stopPropagation();removeAccount(${idx})">${_PL_ICON_DEL}</button>
      </span>
    </div>
  `;
}

// ===== 选中账号 =====
function selectAccount(idx) {
  selectedAccountIdx = idx;
  // highlight in list
  document.querySelectorAll('#pools-list .pa').forEach((el, i) => {
    el.classList.toggle('selected', i === idx);
  });
  renderAccountDetail();
}

// ===== 右侧：账号详情 =====
function renderAccountDetail() {
  const body = document.getElementById('pool-detail-body');
  if (!body) return;

  if (selectedAccountIdx < 0 || !currentPool || !currentPool.accounts) {
    body.innerHTML = `
      <div class="pool-detail-empty">
        <i data-lucide="mouse-pointer-click" style="width:24px;height:24px;color:var(--text-3);"></i>
        <span>${_plT('pools.clickToView', '点击账号查看详情')}</span>
      </div>
    `;
    if (window.lucide) lucide.createIcons();
    return;
  }

  const acc = currentPool.accounts[selectedAccountIdx];
  if (!acc) return;

  const healthCode = acc.healthCode || '';
  const healthDetail = acc.healthStatus === 'unhealthy'
    ? ({AUTH:'鉴权失效', NET:'网络错误', UNKNOWN:'异常'}[healthCode] || (healthCode ? 'HTTP '+healthCode : '异常'))
    : acc.healthStatus === 'healthy'
    ? (healthCode === '429' ? '健康（限流中）' : '健康')
    : '未知';

  const fields = [
    ['邮箱', acc.email || '--'],
    ['提供商', acc.provider || '--'],
    ['区域', acc.region || 'us-east-1'],
    ['订阅', acc.subscription || '--'],
    ['健康状态', healthDetail],
  ];
  if (acc.healthError) fields.push(['健康详情', acc.healthError]);
  fields.push(
    ['信用额度', acc.creditLimit ? `${acc.creditUsed || 0} / ${acc.creditLimit}` : '--'],
    ['额度重置', acc.creditResetAt ? new Date(acc.creditResetAt).toLocaleString() : '--'],
    ['模型数', acc.availableModels ? acc.availableModels.length : 0],
  );

  if (acc.lastUpdatedAt) {
    const d = new Date(acc.lastUpdatedAt);
    fields.push(['最后更新', `${d.getMonth()+1}/${d.getDate()} ${d.getHours()}:${String(d.getMinutes()).padStart(2,'0')}`]);
  }

  body.innerHTML = fields.map(([label, value]) => `
    <div class="pool-detail-field">
      <span class="pool-detail-label">${label}</span>
      <span class="pool-detail-value">${escapeHtml(String(value))}</span>
    </div>
  `).join('') + `
    <button class="btn btn-outline btn-sm" style="margin-top:10px;width:100%;" onclick="exportSelectedAccount()">
      ${_plT('pools.exportOne', '导出此账号')}
    </button>
  `;
}

// ===== 右侧：额度概览 =====
function renderQuotaOverview() {
  const body = document.getElementById('pool-quota-body');
  if (!body) return;

  if (!currentPool || !currentPool.accounts || currentPool.accounts.length === 0) {
    body.innerHTML = '<div style="font-size:12px;color:var(--text-3);padding:8px 0;">暂无数据</div>';
    return;
  }

  const accounts = currentPool.accounts;
  // aggregate quotas across all accounts
  const quotaMap = {};
  accounts.forEach(a => {
    if (a.usageQuotas && a.usageQuotas.quotas) {
      Object.entries(a.usageQuotas.quotas).forEach(([type, q]) => {
        if (!quotaMap[type]) quotaMap[type] = { used: 0, total: 0, remaining: 0 };
        quotaMap[type].used += q.used || 0;
        quotaMap[type].total += q.total || 0;
        quotaMap[type].remaining += q.remaining || 0;
      });
    }
  });

  const types = Object.keys(quotaMap);
  if (types.length === 0) {
    body.innerHTML = '<div style="font-size:12px;color:var(--text-3);padding:8px 0;">暂无额度数据</div>';
    return;
  }

  body.innerHTML = types.map(type => {
    const q = quotaMap[type];
    const pct = q.total > 0 ? Math.min(100, (q.used / q.total) * 100) : 0;
    const color = pct > 80 ? 'red' : pct > 50 ? 'yellow' : 'green';
    return `
      <div class="pool-quota-item">
        <div class="pool-quota-header">
          <span class="pool-quota-name">${escapeHtml(type)}</span>
          <span class="pool-quota-numbers">${q.used.toFixed(0)} / ${q.total.toFixed(0)}</span>
        </div>
        <div class="pool-progress-bar">
          <div class="pool-progress-fill ${color}" style="width:${pct}%"></div>
        </div>
      </div>
    `;
  }).join('');
}

// ===== 右侧：最近活动 =====
function renderActivityFeed() {
  const body = document.getElementById('pool-activity-body');
  if (!body) return;

  if (!currentPool || !currentPool.accounts || currentPool.accounts.length === 0) {
    body.innerHTML = '<div style="font-size:12px;color:var(--text-3);padding:8px 0;">暂无活动</div>';
    return;
  }

  // collect recent events from accounts
  const events = [];
  currentPool.accounts.forEach(a => {
    if (a.lastUpdatedAt) {
      events.push({
        type: a.healthStatus === 'healthy' ? 'green' : a.healthStatus === 'unhealthy' ? 'red' : 'blue',
        text: `${(a.email || 'Unknown').split('@')[0]} 刷新完成`,
        time: new Date(a.lastUpdatedAt)
      });
    }
  });

  // sort by time desc, take 8
  events.sort((a, b) => b.time - a.time);
  const recent = events.slice(0, 8);

  if (recent.length === 0) {
    body.innerHTML = '<div style="font-size:12px;color:var(--text-3);padding:8px 0;">暂无活动</div>';
    return;
  }

  body.innerHTML = recent.map(e => {
    const now = new Date();
    const diff = Math.floor((now - e.time) / 1000 / 60);
    const timeStr = diff < 1 ? '刚刚' : diff < 60 ? diff + '分钟前' : Math.floor(diff / 60) + '小时前';
    return `
      <div class="pool-activity-item">
        <span class="pool-activity-dot ${e.type}"></span>
        <span class="pool-activity-text">${escapeHtml(e.text)}</span>
        <span class="pool-activity-time">${timeStr}</span>
      </div>
    `;
  }).join('');
}

// ===== 导入账号 =====
async function importAccountsToPool() {
  try {
    const filePath = await window.go.main.App.SelectOutlookFile();
    if (!filePath) return;

    const result = await window.go.main.App.ImportToDefaultPool(filePath);
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }

    currentPool = result;
    selectedAccountIdx = -1;
    renderDashboard();
    const total = result.accounts ? result.accounts.length : 0;
    showToast(_plT('pools.importOk', '导入完成，号池共') + ' ' + total, 'success');
  } catch (err) {
    console.error('Failed to import accounts:', err);
    showToast(_plT('pools.importFailed', '导入失败'), 'error');
  }
}

// ===== 删除账号 =====
async function removeAccount(idx) {
  if (!currentPool || !currentPool.accounts) return;
  const acc = currentPool.accounts[idx];
  if (!acc) return;

  const newAccounts = currentPool.accounts.filter((_, i) => i !== idx);
  if (newAccounts.length === 0) {
    await _deleteCurrentPool();
    return;
  }

  try {
    const result = await window.go.main.App.UpdatePool(
      currentPool.id,
      currentPool.name,
      currentPool.strategy,
      JSON.stringify(newAccounts)
    );
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }
    currentPool = result;
    if (selectedAccountIdx === idx) selectedAccountIdx = -1;
    else if (selectedAccountIdx > idx) selectedAccountIdx--;
    renderDashboard();
  } catch (err) {
    console.error('Failed to remove account:', err);
    showToast(_plT('pools.deleteFailed', '删除失败'), 'error');
  }
}

// ===== 清空账号 =====
function clearAllAccounts() {
  if (!currentPool) return;
  if (typeof showConfirmModal === 'function') {
    showConfirmModal(
      _plT('common.clear', '清空'),
      _plT('pools.clearConfirm', '确定要清空所有账号吗？'),
      _plT('common.confirm', '确认'),
      _deleteCurrentPool
    );
  } else {
    _deleteCurrentPool();
  }
}

async function _deleteCurrentPool() {
  if (!currentPool) return;
  try {
    const result = await window.go.main.App.DeletePool(currentPool.id);
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }
    currentPool = null;
    selectedAccountIdx = -1;
    renderDashboard();
    showToast(_plT('pools.cleared', '已清空所有账号'), 'success');
  } catch (err) {
    console.error('Failed to clear accounts:', err);
    showToast(_plT('pools.clearFailed', '清空失败'), 'error');
  }
}

// HTML 转义
function escapeHtml(str) {
  if (!str) return '';
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

// ===== 刷新单个账号 =====
async function refreshSingleAccount(idx) {
  if (!currentPool || !currentPool.accounts) return;
  const acc = currentPool.accounts[idx];
  if (!acc) return;

  const btn = event.target.closest('button');
  if (btn) {
    btn.disabled = true;
    btn.style.opacity = '0.5';
  }

  try {
    const result = await window.go.main.App.RefreshAccountInfo(currentPool.id, acc.email);
    if (result.error) {
      showToast(`刷新失败: ${result.error}`, 'error');
      return;
    }

    currentPool.accounts[idx] = result;
    renderDashboard();
    if (selectedAccountIdx === idx) renderAccountDetail();
    showToast(`已刷新 ${acc.email}`, 'success');
  } catch (err) {
    console.error('Failed to refresh account:', err);
    showToast('刷新失败', 'error');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.style.opacity = '1';
    }
  }
}

// ===== 批量刷新 =====
async function refreshAllAccounts() {
  if (!currentPool) return;

  const btn = event.target.closest('button');
  if (btn) {
    btn.disabled = true;
    btn.innerHTML = '<span style="display:inline-block;animation:spin 1s linear infinite;">⟳</span> 刷新中...';
  }

  try {
    const result = await window.go.main.App.RefreshAllAccountsInfo(currentPool.id);
    if (result.error) {
      showToast(`批量刷新失败: ${result.error}`, 'error');
      return;
    }

    currentPool = result;
    renderDashboard();
    if (selectedAccountIdx >= 0) renderAccountDetail();

    const refreshed = result.refreshed || 0;
    const failed = result.failed || 0;
    showToast(`刷新完成: 成功 ${refreshed}, 失败 ${failed}`, 'success');
  } catch (err) {
    console.error('Failed to refresh all accounts:', err);
    showToast('批量刷新失败', 'error');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerHTML = `
        <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2"/>
        </svg>
        <span>${_plT('pools.refreshAll', '批量刷新')}</span>
      `;
    }
  }
}

// ===== 导出账号（走原生保存对话框：WKWebView 的 <a download> 不会落盘）=====
async function exportAccounts() {
  if (!currentPool || !currentPool.id || !currentPool.accounts || currentPool.accounts.length === 0) {
    showToast(_plT('pools.exportEmpty', '暂无账号可导出'), 'error');
    return;
  }
  try {
    const result = await window.go.main.App.ExportPoolAccounts(currentPool.id);
    if (!result || result.cancelled) return;
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }
    const n = result.count || currentPool.accounts.length;
    showToast(_plT('pools.exported', '已导出') + ' ' + n + ' ' + _plT('accounts.unit', '个'), 'success');
  } catch (err) {
    console.error('Failed to export accounts:', err);
    showToast(_plT('pools.exportFailed', '导出失败'), 'error');
  }
}

async function exportSelectedAccount() {
  if (selectedAccountIdx < 0 || !currentPool || !currentPool.accounts) {
    showToast(_plT('pools.clickToView', '点击账号查看详情'), 'error');
    return;
  }
  const acc = currentPool.accounts[selectedAccountIdx];
  if (!acc) return;
  try {
    const result = await window.go.main.App.ExportPoolAccount(currentPool.id, acc.email || '');
    if (!result || result.cancelled) return;
    if (result.error) {
      showToast(result.error, 'error');
      return;
    }
    showToast(_plT('pools.exported', '已导出') + ' ' + (acc.email || ''), 'success');
  } catch (err) {
    console.error('Failed to export account:', err);
    showToast(_plT('pools.exportFailed', '导出失败'), 'error');
  }
}
