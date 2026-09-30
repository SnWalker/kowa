import { useEffect, useState } from 'react';

type Theme = 'system' | 'light' | 'dark';

export function ThemeSelect() {
  const [theme, setTheme] = useState<Theme>('system');
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return (
    <label className="theme-select">
      <span>外观</span>
      <select value={theme} onChange={(event) => {
        const value = event.target.value;
        if (value === 'system' || value === 'light' || value === 'dark') setTheme(value);
      }}>
        <option value="system">跟随系统</option>
        <option value="light">浅色</option>
        <option value="dark">深色</option>
      </select>
    </label>
  );
}
