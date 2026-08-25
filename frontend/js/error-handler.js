// 全局错误处理器 - 过滤第三方扩展错误

(function() {
  'use strict';

  // 需要过滤的扩展脚本列表
  const EXTENSION_FILTERS = [
    'onetabpro_content.js',
    'extension://',
    'chrome-extension://',
    'moz-extension://',
    'safari-extension://',
    'edge-extension://',
  ];

  // 需要过滤的错误消息关键词
  const ERROR_MESSAGE_FILTERS = [
    'onShown',
    'chrome.runtime',
    'browser.runtime',
  ];

  // 判断是否为扩展错误
  function isExtensionError(error) {
    if (!error) return false;

    // 检查错误堆栈
    const stack = error.stack || '';
    if (EXTENSION_FILTERS.some(filter => stack.includes(filter))) {
      return true;
    }

    // 检查错误消息
    const message = error.message || '';
    if (ERROR_MESSAGE_FILTERS.some(filter => message.includes(filter))) {
      return true;
    }

    // 检查文件名
    if (error.filename) {
      if (EXTENSION_FILTERS.some(filter => error.filename.includes(filter))) {
        return true;
      }
    }

    return false;
  }

  // 拦截 window.onerror
  const originalOnError = window.onerror;
  window.onerror = function(message, source, lineno, colno, error) {
    // 检查是否为扩展错误
    if (source && EXTENSION_FILTERS.some(filter => source.includes(filter))) {
      console.debug('[错误拦截] 已过滤扩展错误:', source);
      return true; // 阻止默认错误处理
    }

    // 检查错误对象
    if (error && isExtensionError(error)) {
      console.debug('[错误拦截] 已过滤扩展错误:', error.message);
      return true;
    }

    // 调用原始错误处理器
    if (originalOnError) {
      return originalOnError.call(this, message, source, lineno, colno, error);
    }

    return false;
  };

  // 拦截 window.addEventListener('error')
  const originalAddEventListener = window.addEventListener;
  window.addEventListener = function(type, listener, options) {
    if (type === 'error') {
      const wrappedListener = function(event) {
        // 检查是否为扩展错误
        if (event.filename && EXTENSION_FILTERS.some(filter => event.filename.includes(filter))) {
          console.debug('[错误拦截] 已过滤扩展错误事件:', event.filename);
          event.stopImmediatePropagation();
          event.preventDefault();
          return;
        }

        if (event.error && isExtensionError(event.error)) {
          console.debug('[错误拦截] 已过滤扩展错误事件:', event.error.message);
          event.stopImmediatePropagation();
          event.preventDefault();
          return;
        }

        // 调用原始监听器
        if (typeof listener === 'function') {
          listener.call(this, event);
        } else if (listener && typeof listener.handleEvent === 'function') {
          listener.handleEvent(event);
        }
      };

      return originalAddEventListener.call(this, type, wrappedListener, options);
    }

    return originalAddEventListener.call(this, type, listener, options);
  };

  // 拦截 Promise 未处理的拒绝
  window.addEventListener('unhandledrejection', function(event) {
    if (event.reason && isExtensionError(event.reason)) {
      console.debug('[错误拦截] 已过滤扩展 Promise 错误:', event.reason.message);
      event.preventDefault();
    }
  });

  // 拦截 console.error（可选）
  const originalConsoleError = console.error;
  console.error = function(...args) {
    // 检查是否包含扩展相关的错误
    const errorString = args.join(' ');
    if (EXTENSION_FILTERS.some(filter => errorString.includes(filter))) {
      console.debug('[错误拦截] 已过滤控制台扩展错误');
      return;
    }

    // 调用原始 console.error
    originalConsoleError.apply(console, args);
  };

  console.log('[错误处理器] 全局错误拦截已启用');
  console.log('[错误处理器] 将自动过滤第三方扩展错误');
})();
