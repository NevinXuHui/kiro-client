// Wails API 适配器 - 将 Wails 调用转换为 Web API 调用

// 创建兼容的 Wails API 对象
window.go = {
  main: {
    App: {
      // 概览相关
      GetOverview: async function() {
        try {
          const status = await window.kiroAPI.getRegisterStatus();
          return {
            version: 'v1.0.0-web',
            running: status.running,
            completed: status.completed,
            failed: status.failed,
            total: status.total
          };
        } catch (err) {
          console.error('GetOverview error:', err);
          return { version: 'v1.0.0-web' };
        }
      },

      // 任务相关
      StartTask: async function(configJson) {
        try {
          const config = JSON.parse(configJson);
          return await window.kiroAPI.startRegister(config);
        } catch (err) {
          console.error('StartTask error:', err);
          return { error: err.message };
        }
      },

      GetTaskStatus: async function() {
        try {
          return await window.kiroAPI.getRegisterStatus();
        } catch (err) {
          console.error('GetTaskStatus error:', err);
          return { running: false, completed: 0, failed: 0, total: 0 };
        }
      },

      // 号池相关
      ListPools: async function() {
        try {
          return await window.kiroAPI.getPoolsList();
        } catch (err) {
          console.error('ListPools error:', err);
          return [];
        }
      },

      ExportPool: async function(poolName, format) {
        try {
          return await window.kiroAPI.exportPool(poolName, format);
        } catch (err) {
          console.error('ExportPool error:', err);
          return { error: err.message };
        }
      },

      RefreshPool: async function(poolName) {
        try {
          return await window.kiroAPI.refreshPool(poolName);
        } catch (err) {
          console.error('RefreshPool error:', err);
          return { error: err.message };
        }
      },

      // 网关相关
      ProxyStart: async function() {
        try {
          return await window.kiroAPI.startGateway(8081);
        } catch (err) {
          console.error('ProxyStart error:', err);
          return { error: err.message };
        }
      },

      ProxyStop: async function() {
        try {
          return await window.kiroAPI.stopGateway();
        } catch (err) {
          console.error('ProxyStop error:', err);
          return { error: err.message };
        }
      },

      ProxyStatus: async function() {
        try {
          return await window.kiroAPI.getGatewayStatus();
        } catch (err) {
          console.error('ProxyStatus error:', err);
          return { running: false };
        }
      },

      ProxyConfig: async function(config) {
        try {
          // Web 版本暂时不支持动态配置
          return { success: true };
        } catch (err) {
          console.error('ProxyConfig error:', err);
          return { error: err.message };
        }
      },

      // 目录和配置相关 (Web 版本使用默认值)
      GetDataDir: async function() {
        return './data';
      },

      SetDataDir: async function(path) {
        return { success: true, message: 'Web version uses default data directory' };
      },

      ResetDataDir: async function() {
        return { success: true };
      },

      GetResultOutputDir: async function() {
        return './results';
      },

      SetResultOutputDir: async function(path) {
        return { success: true, message: 'Web version uses default output directory' };
      },

      ResetResultOutputDir: async function() {
        return { success: true };
      },

      SelectDirectory: async function() {
        // Web 版本无法选择目录
        return '';
      },

      // 代理相关
      GetProxy: async function() {
        return localStorage.getItem('kiro_proxy') || '';
      },

      SetProxy: async function(proxy) {
        localStorage.setItem('kiro_proxy', proxy);
        return { success: true };
      },

      ResetProxy: async function() {
        localStorage.removeItem('kiro_proxy');
        return { success: true };
      },

      DetectProxy: async function(proxy) {
        // 简单验证代理格式
        if (!proxy) return { valid: false, message: 'Empty proxy' };
        if (proxy.match(/^(http|https|socks5):\/\/.+:\d+$/)) {
          return { valid: true, message: 'Proxy format valid' };
        }
        return { valid: false, message: 'Invalid proxy format' };
      },

      // 域名池相关
      ListDomainPool: async function() {
        // Web 版本暂时返回空数组
        return [];
      },

      SetDomainEnabled: async function(domain, enabled) {
        return { success: true };
      },

      UnbanDomain: async function(domain) {
        return { success: true };
      },

      // 模型相关
      TestModelConnection: async function(modelId) {
        // Web 版本暂不支持
        return { success: false, message: 'Not supported in web version' };
      },

      // Outlook 账号相关
      GetOutlookAccounts: async function() {
        // 从 localStorage 读取
        const accounts = localStorage.getItem('kiro_outlook_accounts');
        return accounts ? JSON.parse(accounts) : [];
      },

      AddOutlookAccount: async function(email, password) {
        const accounts = await this.GetOutlookAccounts();
        accounts.push({ email, password, added: new Date().toISOString() });
        localStorage.setItem('kiro_outlook_accounts', JSON.stringify(accounts));
        return { success: true };
      },

      AddOutlookAccounts: async function(data) {
        try {
          // 解析账号数据
          const lines = data.trim().split('\n');
          let addedCount = 0;
          const accounts = await this.GetOutlookAccounts();

          for (const line of lines) {
            const trimmedLine = line.trim();
            if (!trimmedLine || trimmedLine.startsWith('#') || trimmedLine.startsWith('//')) {
              continue;
            }

            // 解析格式：邮箱----密码----ClientID----RefreshToken
            const parts = trimmedLine.split('----');
            if (parts.length < 4) {
              console.warn('跳过格式错误的行:', trimmedLine.substring(0, 50));
              continue;
            }

            const email = parts[0].trim();
            const password = parts[1].trim();
            const field3 = parts[2].trim();
            const field4 = parts[3].trim();

            // 自动识别 ClientID 和 RefreshToken
            let clientId, refreshToken;
            if (field3.startsWith('aorAAAAAG')) {
              refreshToken = field3;
              clientId = field4;
            } else if (field4.startsWith('aorAAAAAG')) {
              clientId = field3;
              refreshToken = field4;
            } else if (field3.includes('nVzLWVhc3QtMQ')) {
              clientId = field3;
              refreshToken = field4;
            } else if (field4.includes('nVzLWVhc3QtMQ')) {
              clientId = field4;
              refreshToken = field3;
            } else {
              clientId = field3;
              refreshToken = field4;
            }

            // 检查是否已存在
            const exists = accounts.some(acc => acc.email === email);
            if (exists) {
              console.log('账号已存在，跳过:', email);
              continue;
            }

            // 添加账号
            accounts.push({
              email,
              password,
              clientId,
              refreshToken,
              registered: false,
              success: false,
              addedAt: new Date().toISOString()
            });
            addedCount++;
          }

          if (addedCount === 0) {
            return { error: '未解析到有效账号或所有账号已存在' };
          }

          // 保存到 localStorage
          localStorage.setItem('kiro_outlook_accounts', JSON.stringify(accounts));

          return {
            added: addedCount,
            total: accounts.length
          };
        } catch (err) {
          console.error('AddOutlookAccounts error:', err);
          return { error: err.message };
        }
      },

      DeleteOutlookAccount: async function(email) {
        let accounts = await this.GetOutlookAccounts();
        const originalLength = accounts.length;
        accounts = accounts.filter(acc => acc.email !== email);
        localStorage.setItem('kiro_outlook_accounts', JSON.stringify(accounts));
        return {
          status: 'deleted',
          total: accounts.length,
          success: accounts.length < originalLength
        };
      },

      RemoveOutlookAccount: async function(email) {
        return await this.DeleteOutlookAccount(email);
      },

      ClearOutlookAccounts: async function() {
        localStorage.setItem('kiro_outlook_accounts', JSON.stringify([]));
        return { status: 'cleared' };
      },

      ClearRegisteredOutlookAccounts: async function() {
        let accounts = await this.GetOutlookAccounts();
        const originalCount = accounts.length;
        accounts = accounts.filter(acc => !acc.registered);
        localStorage.setItem('kiro_outlook_accounts', JSON.stringify(accounts));
        return {
          status: 'ok',
          removed: originalCount - accounts.length,
          total: accounts.length
        };
      },

      SelectOutlookFile: async function() {
        // Web 版本无法直接选择文件
        return '';
      },

      ImportOutlookFile: async function(path) {
        // Web 版本暂不支持文件导入
        return { error: 'Web 版本暂不支持文件导入，请直接粘贴账号数据' };
      },

      // BatchAddProxyEntry 批量添加代理到 Wails adapter
BatchAddProxyEntries: async function(urls, weight) {
  try {
    const response = await fetch(`${this.baseURL}/api/proxy/batch-add`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ urls, weight })
    });
    return await response.json();
  } catch (err) {
    console.error('BatchAddProxyEntries error:', err);
    throw err;
  }
},
      GetHttpAPIAccounts: async function() {
        const accounts = localStorage.getItem('kiro_httpapi_accounts');
        return accounts ? JSON.parse(accounts) : [];
      },

      AddHttpAPIAccount: async function(email, apiUrl) {
        const accounts = await this.GetHttpAPIAccounts();
        accounts.push({ email, apiUrl, added: new Date().toISOString() });
        localStorage.setItem('kiro_httpapi_accounts', JSON.stringify(accounts));
        return { success: true };
      },

      RemoveHttpAPIAccount: async function(email) {
        let accounts = await this.GetHttpAPIAccounts();
        accounts = accounts.filter(acc => acc.email !== email);
        localStorage.setItem('kiro_httpapi_accounts', JSON.stringify(accounts));
        return { success: true };
      }
    }
  }
};

// 初始化完成标志
window.wailsReady = true;

console.log('Wails adapter initialized for web version');
