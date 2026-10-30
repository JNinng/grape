import '@testing-library/jest-dom/vitest'

// Radix UI 在 jsdom 下依赖若干浏览器专有 API：pointer capture 用于 Select
// 的项定位，scrollIntoView / ResizeObserver 用于内容滚动与测量。缺失时
// 直接抛错，这里按无操作垫平。
if (typeof Element.prototype.hasPointerCapture !== 'function') {
  Element.prototype.hasPointerCapture = () => false
}
if (typeof Element.prototype.setPointerCapture !== 'function') {
  Element.prototype.setPointerCapture = () => {}
}
if (typeof Element.prototype.releasePointerCapture !== 'function') {
  Element.prototype.releasePointerCapture = () => {}
}
if (typeof Element.prototype.scrollIntoView !== 'function') {
  Element.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver !== 'function') {
  class ResizeObserverStub implements ResizeObserver {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = ResizeObserverStub
}

// jsdom 未实现 matchMedia（ThemeProvider 跟随系统主题依赖）：按不匹配
// 的静态查询垫平，需要模拟系统主题的用例在用例内覆写。
if (typeof window.matchMedia !== 'function') {
  window.matchMedia = ((query: string): MediaQueryList =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addListener: (): void => {},
      removeListener: (): void => {},
      addEventListener: (): void => {},
      removeEventListener: (): void => {},
      dispatchEvent: (): boolean => false,
    }) as MediaQueryList) as typeof window.matchMedia
}
