// API 客户端 - 用于与后端通信

class KiroAPI {
  constructor(baseURL = '') {
    this.baseURL = baseURL;
    this.ws = null;
    this.wsCallbacks = new Map();
  }

  // HTTP 请求封装
  async request(endpoint, options = {}) {
    const url = `${this.baseURL}/api${endpoint}`;
    const defaultOptions = {
      headers: {
        'Content-Type': 'application/json',
      },
    };

    const response = await fetch(url, { ...defaultOptions, ...options });

    if (!response.ok) {
      const error = await response.json().catch(() => ({ error: 'Request failed' }));
      throw new Error(error.error || `HTTP ${response.status}`);
    }

    return response.json();
  }

  // GET 请求
  async get(endpoint) {
    return this.request(endpoint, { method: 'GET' });
  }

  // POST 请求
  async post(endpoint, data) {
    return this.request(endpoint, {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  // WebSocket 连接
  connectWebSocket(onMessage) {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsURL = `${protocol}//${window.location.host}/api/ws`;

    this.ws = new WebSocket(wsURL);

    this.ws.onopen = () => {
      console.log('WebSocket connected');
    };

    this.ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (onMessage) {
          onMessage(data);
        }
        // 触发特定事件的回调
        if (data.type && this.wsCallbacks.has(data.type)) {
          this.wsCallbacks.get(data.type)(data.data);
        }
      } catch (err) {
        console.error('WebSocket message parse error:', err);
      }
    };

    this.ws.onerror = (error) => {
      console.error('WebSocket error:', error);
    };

    this.ws.onclose = () => {
      console.log('WebSocket disconnected');
      // 自动重连
      setTimeout(() => this.connectWebSocket(onMessage), 3000);
    };
  }

  // 注册 WebSocket 事件回调
  on(eventType, callback) {
    this.wsCallbacks.set(eventType, callback);
  }

  // 发送 WebSocket 消息
  send(data) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(data));
    }
  }

  // 断开 WebSocket
  disconnect() {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  // ===== 注册相关 API =====

  // 开始注册
  async startRegister(config) {
    return this.post('/register/start', config);
  }

  // 停止注册
  async stopRegister() {
    return this.post('/register/stop', {});
  }

  // 获取注册状态
  async getRegisterStatus() {
    return this.get('/register/status');
  }

  // ===== 号池相关 API =====

  // 获取号池列表
  async getPoolsList() {
    return this.get('/pools/list');
  }

  // 导出号池
  async exportPool(poolName, format = 'json') {
    return this.post('/pools/export', { poolName, format });
  }

  // 刷新号池
  async refreshPool(poolName) {
    return this.post('/pools/refresh', { poolName });
  }

  // ===== 网关相关 API =====

  // 启动网关
  async startGateway(port) {
    return this.post('/gateway/start', { port });
  }

  // 停止网关
  async stopGateway() {
    return this.post('/gateway/stop', {});
  }

  // 获取网关状态
  async getGatewayStatus() {
    return this.get('/gateway/status');
  }
}

// 创建全局 API 实例
window.kiroAPI = new KiroAPI();

// 自动连接 WebSocket
window.kiroAPI.connectWebSocket((message) => {
  console.log('WebSocket message:', message);
});
