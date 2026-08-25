// Wails API 适配器 - 将 Wails 调用转换为 Web API 调用

function downloadJSONFile(filename, content) {
  var blob = new Blob([content], { type: 'application/json;charset=utf-8' });
  var url = URL.createObjectURL(blob);
  var a = document.createElement('a');
  a.href = url;
  a.download = filename || 'export.json';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function pickTextFile(accept) {
  return new Promise(function(resolve) {
    var input = document.createElement('input');
    input.type = 'file';
    input.accept = accept || '.json,application/json';
    var settled = false;
    function done(value) {
      if (settled) return;
      settled = true;
      resolve(value);
    }
    input.addEventListener('cancel', function() { done(''); });
    input.onchange = function() {
      var file = input.files && input.files[0];
      if (!file) { done(''); return; }
      var reader = new FileReader();
      reader.onload = function() {
        window._kiroImportText = String(reader.result || '');
        done(file.name || 'upload.json');
      };
      reader.onerror = function() { done(''); };
      reader.readAsText(file);
    };
    input.click();
  });
}


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
          const status = await window.kiroAPI.getRegisterStatus();
          return { kiro: status };
        } catch (err) {
          console.error('GetTaskStatus error:', err);
          return { kiro: { running: false, completed: 0, failed: 0, total: 0 } };
        }
      },

      GetStatus: async function() {
        try {
          return await window.kiroAPI.getRegisterStatus();
        } catch (err) {
          console.error('GetStatus error:', err);
          return { running: false, completed: 0, failed: 0, total: 0, success: 0, elapsed: 0 };
        }
      },

      GetLogs: async function() {
        try {
          const logs = await window.kiroAPI.getLogs();
          return Array.isArray(logs) ? logs : [];
        } catch (err) {
          console.error('GetLogs error:', err);
          return [];
        }
      },

      GetManualRegisterStatus: async function() {
        // Web 版本暂时返回默认状态
        return { running: false };
      },

      StopManualRegister: async function() {
        // Web 版本暂不支持
        return { success: true };
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

      GetPool: async function(poolID) {
        try {
          return await window.kiroAPI.getPool(poolID);
        } catch (err) {
          console.error('GetPool error:', err);
          return { error: err.message };
        }
      },

      RefreshAllAccountsInfo: async function(poolID) {
        try {
          return await window.kiroAPI.refreshAllAccounts(poolID);
        } catch (err) {
          console.error('RefreshAllAccountsInfo error:', err);
          return { error: err.message };
        }
      },

      RefreshAccountInfo: async function(poolID, email) {
        try {
          return await window.kiroAPI.refreshAccount(poolID, email);
        } catch (err) {
          console.error('RefreshAccountInfo error:', err);
          return { error: err.message };
        }
      },

      ExportPoolAccounts: async function(poolID) {
        try {
          var result = await window.kiroAPI.exportPoolAccounts(poolID);
          if (!result || result.error) return result || { error: 'export failed' };
          downloadJSONFile(result.filename, result.content);
          return { success: true, count: result.count };
        } catch (err) {
          console.error('ExportPoolAccounts error:', err);
          return { error: err.message };
        }
      },

      ExportPoolAccount: async function(poolID, email) {
        try {
          var result = await window.kiroAPI.exportPoolAccount(poolID, email);
          if (!result || result.error) return result || { error: 'export failed' };
          downloadJSONFile(result.filename, result.content);
          return { success: true, count: result.count || 1 };
        } catch (err) {
          console.error('ExportPoolAccount error:', err);
          return { error: err.message };
        }
      },

      UpdatePool: async function(id, name, strategy, accountsJSON) {
        try {
          return await window.kiroAPI.updatePool(id, name, strategy, accountsJSON);
        } catch (err) {
          console.error('UpdatePool error:', err);
          return { error: err.message };
        }
      },

      DeletePool: async function(id) {
        try {
          return await window.kiroAPI.deletePool(id);
        } catch (err) {
          console.error('DeletePool error:', err);
          return { error: err.message };
        }
      },

      ImportToDefaultPool: async function(path) {
        try {
          var data = window._kiroImportText || '';
          window._kiroImportText = '';
          if (!data) return { error: '未选择文件' };
          return await window.kiroAPI.importToDefaultPool(data);
        } catch (err) {
          console.error('ImportToDefaultPool error:', err);
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

      // CloudMail 相关
      GetCloudMailConfigs: async function() {
        // Web 版本返回空配置
        return [];
      },

      SaveCloudMailConfig: async function(config) {
        // Web 版本暂不支持
        return { success: false, message: 'Not supported in web version' };
      },

      DeleteCloudMailConfig: async function(domain) {
        // Web 版本暂不支持
        return { success: false, message: 'Not supported in web version' };
      },

      // Outlook 账号：走服务端存储（与注册任务同一数据源）
      GetOutlookAccounts: async function() {
        var list = [];
        try {
          list = await window.kiroAPI.listOutlook() || [];
        } catch (err) {
          console.error('GetOutlookAccounts error:', err);
          list = [];
        }
        if (!Array.isArray(list)) list = [];

        // 一次性把浏览器里旧的 localStorage 账号迁到服务端
        var raw = localStorage.getItem('kiro_outlook_accounts');
        if (list.length === 0 && raw) {
          try {
            var local = JSON.parse(raw);
            if (Array.isArray(local) && local.length) {
              var lines = [];
              for (var i = 0; i < local.length; i++) {
                var a = local[i] || {};
                if (!a.email || !a.password || !a.clientId || !a.refreshToken) continue;
                lines.push([a.email, a.password, a.clientId, a.refreshToken].join('----'));
              }
              if (lines.length) {
                await window.kiroAPI.addOutlook(lines.join('\n'));
                list = await window.kiroAPI.listOutlook() || [];
                if (!Array.isArray(list)) list = [];
              }
            }
          } catch (e) {
            console.error('migrate outlook localStorage failed:', e);
          }
        }
        if (raw) localStorage.removeItem('kiro_outlook_accounts');
        return list;
      },

      AddOutlookAccount: async function(email, password) {
        return window.kiroAPI.addOutlook(email + '----' + password + '---- ----');
      },

      AddOutlookAccounts: async function(data) {
        return window.kiroAPI.addOutlook(data);
      },

      DeleteOutlookAccount: async function(email) {
        return window.kiroAPI.deleteOutlook(email);
      },

      RemoveOutlookAccount: async function(email) {
        return window.kiroAPI.deleteOutlook(email);
      },

      ClearOutlookAccounts: async function() {
        return window.kiroAPI.clearOutlook();
      },

      ClearRegisteredOutlookAccounts: async function() {
        return window.kiroAPI.clearRegisteredOutlook();
      },

      SelectOutlookFile: async function() {
        return pickTextFile('.json,application/json');
      },

      ImportOutlookFile: async function(path) {
        // Web 版本暂不支持文件导入
        return { error: 'Web 版本暂不支持文件导入，请直接粘贴账号数据' };
      },

      ListProxyPool: async function() {
        try {
          return await window.kiroAPI.listProxyPool();
        } catch (err) {
          console.error('ListProxyPool error:', err);
          return [];
        }
      },

      BatchAddProxyEntries: async function(urls, weight) {
        try {
          return await window.kiroAPI.batchAddProxy(urls, weight);
        } catch (err) {
          console.error('BatchAddProxyEntries error:', err);
          throw err;
        }
      },

      AddProxyEntry: async function(name, url, weight) {
        return window.kiroAPI.addProxy(name, url, weight);
      },

      UpdateProxyEntry: async function(id, name, url, weight, enabled) {
        return window.kiroAPI.updateProxy(id, name, url, weight, enabled);
      },

      DeleteProxyEntry: async function(id) {
        return window.kiroAPI.deleteProxy(id);
      },

      BatchDeleteProxyEntries: async function(ids) {
        return window.kiroAPI.batchDeleteProxy(ids);
      },

      BatchSetProxyWeight: async function(ids, weight) {
        return window.kiroAPI.batchSetProxyWeight(ids, weight);
      },

      TestProxyEntry: async function(url) {
        return window.kiroAPI.testProxy(url);
      },

      DetectProxy: async function(proxyStr) {
        return window.kiroAPI.testProxy(proxyStr);
      },

      // HTTP 邮箱：走服务端存储（与注册任务同一数据源）
      GetHttpAPIAccounts: async function() {
        var list = [];
        try {
          list = await window.kiroAPI.listHttpAPI() || [];
        } catch (err) {
          console.error('GetHttpAPIAccounts error:', err);
          list = [];
        }
        if (!Array.isArray(list)) list = [];

        var raw = localStorage.getItem('kiro_httpapi_accounts');
        if (list.length === 0 && raw) {
          try {
            var local = JSON.parse(raw);
            if (Array.isArray(local) && local.length) {
              var lines = [];
              for (var i = 0; i < local.length; i++) {
                var a = local[i] || {};
                if (!a.email || !a.apiUrl) continue;
                lines.push(a.email + '----' + a.apiUrl);
              }
              if (lines.length) {
                await window.kiroAPI.addHttpAPI(lines.join('\n'));
                list = await window.kiroAPI.listHttpAPI() || [];
                if (!Array.isArray(list)) list = [];
              }
            }
          } catch (e) {
            console.error('migrate httpapi localStorage failed:', e);
          }
        }
        if (raw) localStorage.removeItem('kiro_httpapi_accounts');
        return list;
      },

      AddHttpAPIAccounts: async function(data) {
        return window.kiroAPI.addHttpAPI(data);
      },

      AddHttpAPIAccount: async function(email, apiUrl) {
        return window.kiroAPI.addHttpAPI(email + '----' + apiUrl);
      },

      DeleteHttpAPIAccount: async function(email) {
        return window.kiroAPI.deleteHttpAPI(email);
      },

      RemoveHttpAPIAccount: async function(email) {
        return window.kiroAPI.deleteHttpAPI(email);
      },

      ClearHttpAPIAccounts: async function() {
        return window.kiroAPI.clearHttpAPI();
      },

      ClearRegisteredHttpAPIAccounts: async function() {
        return window.kiroAPI.clearRegisteredHttpAPI();
      },

      ImportHttpAPIFile: async function(path) {
        return { error: 'Web 版本暂不支持文件导入，请直接粘贴账号数据' };
      }
    }
  }
};

// 初始化完成标志
window.wailsReady = true;

console.log('Wails adapter initialized for web version');
