// ===== 国际化 (i18n) =====
// 翻译字典 + t/applyI18n/setLanguage/init
// 用法:
//   - HTML 文本: <span data-i18n="nav.overview">概览</span>
//   - HTML placeholder: <input data-i18n-placeholder="form.search" placeholder="搜索">
//   - HTML title: <span data-i18n-title="tip.help" title="帮助">?</span>
//   - JS: t('toast.saved') / t('toast.deleted', {n: 3})

(function(){
  'use strict';

  var DICT = {
    zh: {
      nav: {
        overview: '主页', masterlog: '总日志', register: '注册机', accounts: '邮箱池', pools: '号池', subscription: '订阅',
        gateway: '网关', models: '模型',
        about: '关于', settings: '设置', toggleTheme: '切换主题', checkUpdate: '检查更新'
      },
      page: {
        overview: '主页', masterlog: '总日志', register: '注册机', accounts: '邮箱池', pools: '号池', subscription: '订阅',
        gateway: '网关', models: '模型',
        about: '关于', settings: '设置'
      },
      common: {
        loading: '加载中...', loadFailed: '加载失败', noData: '暂无数据', notSet: '未配置',
        general: '基本配置',
        copy: '复制', save: '保存', cancel: '取消', confirm: '确认', delete: '删除',
        reset: '重置', clear: '清除', clearAll: '清空全部', select: '选择', close: '关闭',
        ok: '确定', edit: '编辑', add: '添加', test: '测试', refresh: '刷新',
        retry: '重试', back: '返回', next: '下一步', prev: '上一步', open: '打开',
        all: '全部', random: '随机', poll: '轮询', enabled: '已启用', disabled: '未启用',
        prevPage: '上一页', nextPage: '下一页'
      },
      status: {
        idle: '空闲', running: '运行中', success: '成功', failed: '失败',
        registered: '已注册', unregistered: '未注册', pending: '待获取', fetching: '获取中',
        ready: '已就绪', suspended: '已封禁', tested: '已测试', untested: '未测试',
        available: '可用', unavailable: '不可用', configured: '已配置', notConfigured: '未配置',
        connecting: '连接中', detecting: '检测中', notStarted: '未开始'
      },
      overview: {
        kiroAccounts: '号池账号', poolHealthy: '健康', poolUnhealthy: '异常', models: '模型数',
        successRate: '注册成功率',
        liveStatus: '实时状态', progress: '进度', success: '成功', failed: '失败',
        elapsed: '已耗时', eta: '预计剩余', avg: '平均耗时', rate: '成功率',
        newTask: '新建任务', stop: '停止',
        quickActions: '快捷操作', activity: '实时动态',
        activityHint: '任务运行后这里会显示实时动态',
        latest: '最新', earlier: '更早'
      },
      about: {
        currentVersion: '当前版本', latestVersion: '最新版本', releaseDate: '发布日期', author: '作者',
        newVersionFound: '发现新版本', joinGroup: '加入交流群', updateContent: '更新内容',
        updateNow: '立即更新', features: '版本特性', clickToUpdate: '点击立即更新到最新版本',
        wechatPay: '微信支付', alipay: '支付宝'
      },
      settings: {
        appearance: '外观',
        general: '常规', notification: '通知',
        dataDir: '存储目录', dataDirDesc: 'Outlook 账号池等内部数据的本地存储位置',
        dataDirPlaceholder: '默认存储路径',
        outputDir: '注册结果输出目录', outputDirDesc: '成功账号以明文 JSON 数组写入该目录下的 accounts.json',
        outputDirPlaceholder: '默认：应用所在目录',
        proxy: '出口代理',
        proxyDesc: '网关上游请求（模型对话）均走该代理；留空=直连（直连易被 Kiro 限流）。支持 http/https/socks5 完整 URL，也支持 host:port:user:pass、host:port、user:pass@host:port 等简写。',
        proxyPlaceholder: '例如 http://user:pass@127.0.0.1:7890',
        testProxy: '测试连通',
        proxyPool: '代理池', proxyPoolDesc: '供注册机（Outlook 注册任务）使用的代理列表，按权重轮询选取。',
        domainPool: '域名池', domainPoolDesc: '自动同步 Cloud-Mail 服务器域名；被 TES 拉黑的域名自动标记并跳过注册，避免批量注册风控。'
      },
      domainpool: {
        empty: '暂无域名，请先在邮箱池页添加并测试 Cloud-Mail 配置',
        active: '正常', disabled: '已禁用', banned: '已拉黑',
        servers: '台服务器',
        unban: '解除拉黑', unbanOk: '已解除拉黑，域名恢复使用',
        disable: '禁用', enable: '启用'
      },
      logs: { title: '运行日志', copyLog: '复制日志', empty: '暂无日志' },
      masterlog: { title: '总日志', copyLog: '复制日志', empty: '暂无日志' },
      register: {
        newTask: '新建注册任务', count: '注册数量', concurrency: '并发数', delay: '延迟 (秒)',
        emailProvider: '邮箱提供商', outlook: 'Outlook', httpapi: 'HTTP 邮箱', cloudmail: 'Cloud-Mail',
        selectDomain: '选择域名', selectAllDomain: '全选域名',
        domainHint: '邮箱名将自动生成随机字符串',
        outlookHint: '使用 Outlook 账号注册。卡密：邮箱----密码----ClientID----RefreshToken（后两段可对调）',
        httpapiHint: '使用 HTTP API 邮箱注册。卡密：邮箱----API地址（支持「邮箱：」前缀）',
        httpapiHintFull: '通过 HTTP GET 拉取邮件并提取 6 位验证码。ourmail.top 等接口可直接粘贴。',
        cloudmailHint: '使用 Cloud-Mail 自部署邮箱注册。⚠️ 每次注册会创建永久账号，需手动清理。',
        cloudmailWarn: '⚠️ 每次注册会在 Cloud-Mail 上创建一个永久账号，需手动清理',
        outlookHintFull: '卡密：邮箱----密码----ClientID----RefreshToken，后两段顺序可对调。程序用 refresh_token 换 access_token，IMAP XOAUTH2 收信。',
        modeRandom: '随机', modeRoundRobin: '轮询',
        startBtn: '开始注册', stopBtn: '停止',
        realtimeLog: '实时日志',
        manualTitle: '手动注册（浏览器）', manualDesc: '弹出真实浏览器窗口，你在浏览器里手动完成 AWS Builder ID 注册 + 授权（真人操作可绕过 TES 风控与域名拉黑）。程序自动轮询令牌，注册成功后自动入库号池。',
        manualSteps: '步骤：', manualStart: '开始手动注册'
      },
      accounts: {
        cloudmailTitle: 'Cloud-Mail 自部署邮箱', addConfig: '添加新配置',
        configName: '名称', optional: '(可选)', configNamePlaceholder: '自动生成',
        apiUrl: 'API URL', apiKey: 'API Key',
        testConnection: '测试连接', addConfigBtn: '添加配置',
        outlookTitle: 'Outlook', httpapiTitle: 'HTTP 邮箱', count: '共', countUnit: '个',
        addAccount: '添加账号', clearRegistered: '清除已注册',
        httpapiModalTitle: '添加 HTTP 邮箱',
        httpapiFormat: '格式：邮箱----API地址，每行一个（支持「邮箱：」前缀）',
        httpapiPlaceholder: 'user@icloud.com----https://host/api?...',
        httpapiInputRequired: '请粘贴账号（邮箱----API地址）',
        clearAllHttpAPITitle: '清空 HTTP 邮箱',
        clearAllHttpAPIMsg: '确认清空所有 HTTP 邮箱账号？此操作不可恢复！',
        thIndex: '#', thEmail: '邮箱地址', thStatus: '状态', thAddedAt: '添加时间', thActions: '操作',
        addModalTitle: '添加 Outlook 账号',
        importFile: '导入文件', selectTxt: '选择 TXT 文件', perLine: '每行一个账号',
        orManual: '或手动输入', manualInput: '手动输入',
        manualFormat: '格式：邮箱----密码----ClientID----RefreshToken，每行一个',
        manualPlaceholder: 'user@outlook.com----password----clientid----refreshtoken',
        addToList: '添加到列表',
        configList: '配置列表', configList2: '配置列表',
        addNewConfig: '添加新配置',
        thName: '名称', thUrl: 'URL',
        inputRequired: '请先输入 Outlook 账号数据',
        addedSummary: '成功添加 {n} 个账号，当前共 {total} 个',
        importSummary: '成功导入 {n} 个账号，当前共 {total} 个',
        importFailed: '导入失败',
        pagerInfo: '第 {cur} / {total} 页 (共 {n} 个)',
        emptyRow: '暂无邮箱账号',
        deleteTitle: '删除账号',
        deleteMsg: '确认删除账号 {email} ?',
        deleteConfirm: '确认删除',
        deletedOne: '账号已删除',
        clearAllTitle: '清空 Outlook 账号',
        clearAllMsg: '确认清空所有 Outlook 账号？此操作不可恢复！',
        clearAllConfirm: '确认清空',
        allCleared: '已清空所有账号',
        noRegistered: '没有已注册的账号',
        clearRegisteredTitle: '清除已注册',
        clearRegisteredMsg: '确认删除 {n} 个已注册（成功/失败）的账号？'
      },
      subscription: {
        accountList: '账号列表', autoLoaded: '已自动加载输出文件夹中的账号',
        autoLoadedFrom: '已自动加载：{dir}',
        batchGet: '一键获取选中', copyLinks: '复制选中链接', reload: '重新加载账号',
        concurrency: '并发', notStarted: '未开始',
        thEmail: '邮箱', thSubscription: '订阅', thStatus: '状态', thActions: '操作',
        planModalTitle: '选择订阅计划', planLoading: '将使用账号 — 加载中…',
        startBatch: '开始获取',
        errorModalTitle: '获取失败 · 上游响应', errorAccount: '账号',
        loadFailed: '加载账号失败',
        totalSelected: '共 {total} 个 / 已选 {sel}',
        statRunning: '进行中 {n}', statSuccess: '成功 {n}',
        statSuspended: '封禁 {n}', statFailed: '失败 {n}',
        emptyOutput: '输出目录下尚无账号，请先注册或调整输出目录。',
        clickForDetail: '点击查看详情', clickForResponse: '点击查看详细响应',
        openLink: '打开链接', copyLink: '复制链接',
        fetch: '获取', refetch: '重新获取',
        pickFirst: '请先勾选要获取的账号',
        planHintSingle: '将使用账号 {email} 加载可用计划，并仅为该账号获取链接。',
        planHintBatch: '将使用账号 {email} 加载可用计划，并对已勾选的 {n} 个账号批量获取链接。',
        noPlans: '未返回任何可用计划',
        pickPlan: '请先选择一个计划',
        loadPickPlan: '请先加载并选择计划',
        bannedRemoved: '账号 {email} 已被封禁，已从输出文件移除',
        bannedShort: '账号已被封禁',
        unknownError: '未知错误',
        linkCopied: '已复制链接',
        linksCopied: '已复制 {n} 条链接',
        noLinksToCopy: '暂无可复制的链接（需勾选且已获取成功）',
        noErrorInfo: '(无错误信息)',
        errCopied: '已复制错误详情'
      },
      modal: {
        updateTitle: '发现新版本', updateLater: '稍后', updateDownload: '前往下载',
        confirmLogoutTitle: '确认退出卡密？',
        confirmLogoutMsg: '此操作会清除本地授权信息，需要重新激活卡密才能使用。',
        confirmLogoutBtn: '确认退出'
      },
      toast: {
        saved: '已保存', deleted: '已删除', cleared: '已清空', copied: '已复制',
        copyFailed: '复制失败', operationOk: '操作成功', operationFailed: '操作失败',
        proxySaved: '代理已保存', proxyCleared: '代理已清除', proxyDetecting: '检测代理中...', proxyEmpty: '请先填写代理地址',
        dataDirSet: '存储目录已设置', dataDirReset: '已重置为默认存储目录',
        outputDirSet: '输出目录已设置', outputDirReset: '已重置为默认输出目录',
        emptyDir: '请选择目录',
        addOk: '添加成功', addFailed: '添加失败',
        deleteOk: '删除成功', deleteFailed: '删除失败',
        clearOk: '清空成功', clearFailed: '清空失败',
        testing: '测试中...', testOk: '连接成功', testFailed: '连接失败',
        accountsAdded: '已添加 {n} 个账号', accountsDeleted: '已删除 {n} 个账号',
        clearedCount: '已清空 {n} 项',
        confirmDelete: '确认删除？', confirmClear: '确认清空全部？',
        importOk: '导入成功 ({n} 个)', importFailed: '导入失败',
        taskStarting: '任务启动中...', taskRunning: '任务运行中', taskStopped: '任务已停止',
        taskCompleted: '任务完成', taskFailed: '任务失败',
        taskStarted: '任务已启动', taskStartFailed: '启动失败',
        taskStopping: '正在停止任务...', taskStopFailed: '停止失败',
        upToDate: '当前已是最新版本', checkUpdateFailed: '检查更新失败',
        taskCompleteMsg: '{name} 任务完成！成功 {s} / 失败 {f} / 共 {t}',
        configMissing: '配置缺失', selectAtLeastOne: '请至少选择一个',
        noAvailableAccount: '没有可用账号', noEmailSelected: '未选择邮箱',
        logCopied: '日志已复制', logEmpty: '暂无日志'
      },
      cloudmail: {
        summaryNone: '未配置',
        summaryActive: '已配置 {n} 个，可用 {m} 个',
        emptyInline: '暂无配置，请在上方添加 Cloud-Mail 配置',
        autoNamePrefix: '配置',
        adminEmail: '管理员邮箱',
        adminPassword: '管理员密码',
        domains: '允许的域名 (每行/逗号分隔)',
        permanentWarn: '⚠️ Cloud-Mail 没有公开删除接口，每次注册创建的邮箱账号会永久保留在服务器上，需手动清理。',
        requiredFields: '请填写 URL、管理员邮箱、密码',
        requiredDomains: '请填写至少一个域名',
        nameExists: '配置名称已存在',
        testing: '测试中...',
        testFailed: '连接失败',
        testFailedShort: '测试失败',
        connectedDomains: '连接成功，{n} 个域名',
        connectedDomainsList: '连接成功，域名: {d}',
        connectedNoDomain: '连接成功，但服务器未返回域名（可能开启了 loginDomain 隐私开关）',
        testOkWithDomains: '连接成功，{n} 个域名',
        testOkNoDomain: '连接成功，但服务器未返回域名',
        domainsOptional: '允许的域名（可选，留空将自动从服务器拉取）',
        addedNamed: '已添加: {name}',
        deleteConfigTitle: '删除配置',
        deleteConfigMsg: '确认删除配置 "{name}" 吗？',
        clearAllTitle: '清空 Cloud-Mail 配置',
        clearAllMsg: '确认清空所有 Cloud-Mail 配置吗？此操作不可恢复。',
        nothingToClear: '没有配置可清空',
        allCleared: '已清空所有配置',
        noDomainsHint: '暂无配置，请先在邮箱池页添加',
        noActiveDomain: '暂无可用域名，请先测试 Cloud-Mail 配置'
      },
      pools: {
        title: '号池管理',
        list: '账号列表',
        importAccounts: '添加账号',
        empty: '暂无账号',
        emptyHint: '点击右上角"添加账号"导入 JSON 文件',
        current: '当前号池',
        validHint: '有效账号 / 总数',
        importOk: '导入完成，号池共',
        importFailed: '导入失败',
        deleteFailed: '删除失败',
        clearConfirm: '确定要清空所有账号吗？',
        cleared: '已清空所有账号',
        clearFailed: '清空失败',
        refreshAll: '批量刷新',
        totalAccounts: '总账号',
        healthy: '健康',
        unhealthy: '异常',
        unknown: '未知',
        availableModels: '可用模型',
        usageTrend: '账号使用趋势',
        usageSubtitle: '按账号的额度使用情况',
        healthDistribution: '健康分布',
        modelDistribution: '模型分布',
        accountList: '账号列表',
        clearAll: '清空',
        accountDetail: '账号详情',
        quotaOverview: '额度概览',
        recentActivity: '最近活动',
        quickActions: '快捷操作',
        clickToView: '点击账号查看详情',
        refreshAllAccounts: '刷新全部账号',
        import: '导入账号',
        export: '导出账号',
        exported: '已导出',
        exportEmpty: '暂无账号可导出',
        exportFailed: '导出失败',
        exportOne: '导出此账号',
        clearAllAccounts: '清空全部账号',
        used: '已使用',
        total: '总额度',
        accountCount: '账号数'
      },
      gateway: {
        control: '网关控制',
        status: '网关状态',
        statusDesc: '开启后可通过本地端口访问 Kiro API',
        start: '开启网关',
        stop: '停止网关',
        stopped: '已停止',
        running: '运行中',
        port: '端口设置',
        portDesc: '网关监听的本地端口号',
        endpoint: '接入地址',
        apiUrl: 'API 地址',
        apiUrlDesc: '复制此地址到其他软件中使用（如 Cursor、Windsurf 等）',
        apiKey: 'API Key',
        apiKeyDesc: '其他软件连接时使用的密钥，默认 123，可自行修改'
      },
      models: {
        available: '可用模型',
        copyId: '复制 ID'
      }
    }
  };

  var DEFAULT_LANG = 'zh';
  var currentLang = DEFAULT_LANG;

  function getByPath(obj, path) {
    var parts = path.split('.');
    var cur = obj;
    for (var i = 0; i < parts.length; i++) {
      if (cur == null) return undefined;
      cur = cur[parts[i]];
    }
    return cur;
  }

  function interpolate(s, vars) {
    if (!vars) return s;
    return s.replace(/\{(\w+)\}/g, function(_, k) {
      return vars[k] != null ? vars[k] : '{' + k + '}';
    });
  }

  function t(key, vars) {
    var v = getByPath(DICT[currentLang], key);
    if (v == null) v = getByPath(DICT[DEFAULT_LANG], key);
    if (v == null) return key;
    return interpolate(v, vars);
  }

  function applyI18n(root) {
    root = root || document;
    // textContent
    var nodes = root.querySelectorAll('[data-i18n]');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      var key = el.getAttribute('data-i18n');
      var val = t(key);
      // 保留 textContent 模式：完全替换
      el.textContent = val;
    }
    // placeholder
    var phs = root.querySelectorAll('[data-i18n-placeholder]');
    for (var j = 0; j < phs.length; j++) {
      phs[j].setAttribute('placeholder', t(phs[j].getAttribute('data-i18n-placeholder')));
    }
    // title (tooltips)
    var titles = root.querySelectorAll('[data-i18n-title]');
    for (var k = 0; k < titles.length; k++) {
      titles[k].setAttribute('title', t(titles[k].getAttribute('data-i18n-title')));
    }
  }

  // 仅保留中文：语言固定 zh，无切换
  function setLanguage() {
    currentLang = DEFAULT_LANG;
    document.documentElement.setAttribute('lang', DEFAULT_LANG);
    applyI18n(document);
  }

  function getLanguage() { return DEFAULT_LANG; }

  async function init() {
    setLanguage();
  }

  // translateLog: 中文模式直接返回原文。
  function translateLog(text) {
    return text;
  }

  window.I18N = {
    t: t,
    applyI18n: applyI18n,
    setLanguage: setLanguage,
    getLanguage: getLanguage,
    init: init,
    DICT: DICT,
    translateLog: translateLog
  };
  // 顶层快捷函数
  window.t = t;
})();
