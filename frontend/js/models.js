// ===== 模型管理 =====

// Kiro 支持的模型列表（来自 9router registry）
const KIRO_MODELS = [
  { id: 'claude-opus-4.8', name: 'Claude Opus 4.8', provider: 'Anthropic' },
  { id: 'claude-opus-4.7', name: 'Claude Opus 4.7', provider: 'Anthropic' },
  { id: 'claude-opus-4.5', name: 'Claude Opus 4.5', provider: 'Anthropic' },
  { id: 'claude-sonnet-5', name: 'Claude Sonnet 5', provider: 'Anthropic' },
  { id: 'claude-sonnet-4.5', name: 'Claude Sonnet 4.5', provider: 'Anthropic' },
  { id: 'claude-haiku-4.5', name: 'Claude Haiku 4.5', provider: 'Anthropic' },
  { id: 'deepseek-3.2', name: 'DeepSeek 3.2', provider: 'DeepSeek' },
  { id: 'qwen3-coder-next', name: 'Qwen3 Coder Next', provider: 'Alibaba' },
  { id: 'glm-5', name: 'GLM 5', provider: 'Zhipu AI' },
  { id: 'MiniMax-M2.5', name: 'MiniMax M2.5', provider: 'MiniMax' },
];

// 初始化
window.addEventListener('DOMContentLoaded', function() {
  renderModels();
});

// 每个模型的测试状态（同 9router modelTestResults）
const modelTestResults = {};
const modelTesting = {};

// 渲染模型列表
function renderModels() {
  const container = document.getElementById('models-list');
  if (!container) return;

  // 更新模型计数
  const countEl = document.getElementById('models-count');
  if (countEl) {
    countEl.textContent = KIRO_MODELS.length;
  }

  container.innerHTML = KIRO_MODELS.map((model, idx) => {
    const res = modelTestResults[model.id];
    let statusHtml = '';
    if (modelTesting[model.id]) {
      statusHtml = `<span style="display:inline-flex;align-items:center;gap:4px;font-size:11px;color:var(--text-2);">
        <span style="display:inline-block;width:12px;height:12px;border:2px solid var(--line);border-top-color:var(--blue);border-radius:50%;animation:spin 1s linear infinite;"></span>测试中</span>`;
    } else if (res) {
      if (res.ok) {
        statusHtml = `<span style="display:inline-flex;align-items:center;gap:4px;font-size:11px;color:var(--green);">
          <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5"/></svg>
          ${res.latencyMs}ms</span>`;
      } else {
        statusHtml = `<span style="display:inline-flex;align-items:center;gap:4px;font-size:11px;color:var(--red);max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="${escapeHtml(res.error || '')}">
          <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6L6 18M6 6l12 12"/></svg>
          ${escapeHtml((res.error || '').slice(0, 40))}</span>`;
      }
    }
    return `
    <div class="model-card" style="
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 12px 16px;
      background: var(--surface);
      border: 1px solid ${res && res.ok ? 'var(--green)' : (res && !res.ok ? 'var(--red-dim)' : 'var(--line)')};
      border-radius: var(--radius-md);
      transition: all 0.2s;
    ">
      <div class="model-icon" style="
        width: 36px;
        height: 36px;
        border-radius: 10px;
        background: var(--blue-dim);
        display: flex;
        align-items: center;
        justify-content: center;
        flex-shrink: 0;
      ">
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="var(--blue)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
        </svg>
      </div>
      <div class="model-info" style="flex:1;min-width:0;">
        <div class="model-name" style="font-size:13px;font-weight:600;color:var(--ink);margin-bottom:2px;">
          ${escapeHtml(model.name)}
        </div>
        <div class="model-meta" style="font-size:11px;color:var(--text-3);display:flex;gap:8px;align-items:center;">
          <span style="font-family:var(--font-mono);color:var(--text-2);">${escapeHtml(model.id)}</span>
          <span>·</span>
          <span>${escapeHtml(model.provider)}</span>
          ${statusHtml}
        </div>
      </div>
      <button class="btn btn-outline btn-sm" onclick="testModel('${escapeHtml(model.id)}')" id="test-btn-${escapeHtml(model.id)}" style="gap:4px;">
        <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M9 3h6M10 3v5.5L4.8 18a2 2 0 0 0 1.8 3h10.4a2 2 0 0 0 1.8-3L14 8.5V3"/>
        </svg>
        <span>测试</span>
      </button>
      <button class="btn btn-outline btn-sm" onclick="copyModelId('${escapeHtml(model.id)}')" style="gap:4px;">
        <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
        </svg>
        <span>复制</span>
      </button>
    </div>
  `;
  }).join('');
}

// 测试模型连通性（真实发一次上游请求）
async function testModel(modelId) {
  if (modelTesting[modelId]) return;
  modelTesting[modelId] = true;
  delete modelTestResults[modelId];
  renderModels();

  const btn = document.getElementById('test-btn-' + modelId);
  if (btn) {
    btn.innerHTML = '<span style="display:inline-block;width:12px;height:12px;border:2px solid var(--line);border-top-color:var(--blue);border-radius:50%;animation:spin 1s linear infinite;"></span>';
    btn.disabled = true;
  }

  try {
    const result = await window.go.main.App.TestModelConnection(modelId);
    modelTestResults[modelId] = result || { ok: false, error: '无响应' };
    if (result && result.ok) {
      showToast(`测试成功（${result.latencyMs}ms）`, 'success');
    } else {
      showToast(`测试失败：${(result && result.error) || '未知错误'}`, 'error');
    }
  } catch (err) {
    modelTestResults[modelId] = { ok: false, error: err.message || String(err) };
    showToast('测试失败', 'error');
  } finally {
    modelTesting[modelId] = false;
    renderModels();
  }
}

// 复制模型 ID
function copyModelId(modelId) {
  const textarea = document.createElement('textarea');
  textarea.value = modelId;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand('copy');
  document.body.removeChild(textarea);
  showToast(`已复制: ${modelId}`, 'success');
}

// 刷新模型列表
function refreshModels() {
  renderModels();
  showToast('模型列表已刷新', 'success');
}

// HTML 转义
function escapeHtml(str) {
  if (!str) return '';
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}
