export {};

declare global {
  interface AetherWailsRuntime {
    WindowSetSize(width: number, height: number): void;
    WindowGetSize(): Promise<{ w: number; h: number }>;
    WindowSetMinSize(width: number, height: number): void;
    WindowSetAlwaysOnTop(on: boolean): void;
    WindowIsMaximised(): Promise<boolean>;
    WindowMaximise(): void;
    WindowUnmaximise(): void;
    WindowMinimise(): void;
    WindowHide(): void;
    Quit(): void;
  }
  interface Window {
    /** Wails 只在桌面 WebView 内注入；浏览器里不存在。 */
    runtime?: AetherWailsRuntime;
    /** 渠道页挂给外壳「＋」按钮的回调。 */
    __aetherAddProvider?: () => void;
  }
}
