// ===== 网关管理 =====

let gatewayRunning = false;

// 初始化
window.addEventListener('DOMContentLoaded', function() {
  loadGatewayConfig();
  updateGatewayUrl();
});

// 加载网关配置
async function loadGatewayConfig() {
  try {
    // 获取端口设置
    const status = await window.go.main.App.ProxyStatus();
    if (status && status.port) {
      document.getElementById('gw-port').value = status.port;
    }
    if (status && status.apiKey) {
      document.getElementById('gw-key').value = status.apiKey;
    }

    // 更新网关状态
    updateGatewayStatus(status.running);
    updateGatewayUrl();
  } catch (err) {
    console.error('Failed to load gateway config:', err);
  }
}

// 更新网关状态显示
function updateGatewayStatus(running) {
  gatewayRunning = running;
  const dot = document.getElementById('gw-status-dot');
  const text = document.getElementById('gw-status-text');
  const btn = document.getElementById('gw-toggle-btn');

  if (running) {
    dot.className = 'dot running';
    text.textContent = '运行中';
    text.style.color = 'var(--green)';
    btn.className = 'btn btn-outline';
    btn.innerHTML = '<i data-lucide="power"></i><span>停止网关</span>';
  } else {
    dot.className = 'dot idle';
    text.textContent = '已停止';
    text.style.color = 'var(--text-3)';
    btn.className = 'btn btn-primary';
    btn.innerHTML = '<i data-lucide="power"></i><span>开启网关</span>';
  }

  if (window.lucide) lucide.createIcons();
}

// 更新网关 URL
function updateGatewayUrl() {
  const port = document.getElementById('gw-port').value || 20130;
  const url = `http://127.0.0.1:${port}/v1`;
  document.getElementById('gw-url').value = url;
}

// 切换网关状态
async function toggleGateway() {
  try {
    if (gatewayRunning) {
      const result = await window.go.main.App.ProxyStop();
      if (result.error) {
        showToast(result.error, 'error');
        return;
      }
      updateGatewayStatus(false);
      showToast('网关已停止', 'success');
    } else {
      // 先保存端口
      const port = parseInt(document.getElementById('gw-port').value);
      if (port && port > 0) {
        await window.go.main.App.ProxyConfig({ port: port });
      }

      const result = await window.go.main.App.ProxyStart();
      if (result.error) {
        showToast(result.error, 'error');
        return;
      }
      updateGatewayStatus(true);
      showToast('网关已启动', 'success');
    }
  } catch (err) {
    console.error('Failed to toggle gateway:', err);
    showToast('操作失败', 'error');
  }
}

// 保存 API Key
async function saveGatewayKey() {
  try {
    const key = document.getElementById('gw-key').value.trim();
    if (!key) {
      showToast('API Key 不能为空', 'error');
      return;
    }
    await window.go.main.App.ProxyConfig({ apiKey: key });
    showToast('API Key 已保存', 'success');
  } catch (err) {
    console.error('Failed to save api key:', err);
    showToast('保存失败', 'error');
  }
}

// 保存端口设置
async function saveGatewayPort() {
  try {
    const port = parseInt(document.getElementById('gw-port').value);
    if (!port || port < 1024 || port > 65535) {
      showToast('端口范围：1024-65535', 'error');
      return;
    }

    await window.go.main.App.ProxyConfig({ port: port });
    updateGatewayUrl();
    showToast('端口已保存', 'success');
  } catch (err) {
    console.error('Failed to save port:', err);
    showToast('保存失败', 'error');
  }
}

// 复制网关 URL
function copyGatewayUrl() {
  const urlInput = document.getElementById('gw-url');
  urlInput.select();
  document.execCommand('copy');
  showToast('已复制到剪贴板', 'success');
}
