import { useEffect, useState } from 'react';
import { Label } from '../ui/label';
import { Select } from '../ui/Select';

type Theme = 'system' | 'light' | 'dark';

/**
 * Appearance is a document property, not application state: the control writes
 * `data-theme` and nothing else. It is never persisted, so a new session always
 * starts from the system preference.
 */
export function ThemeSelect() {
  const [theme, setTheme] = useState<Theme>('system');
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return (
    <Label className="flex items-center gap-2 text-xs font-normal whitespace-nowrap text-muted-foreground select-text">
      <span>外观</span>
      <Select
        value={theme}
        className="w-auto"
        onChange={(event) => {
          const value = event.target.value;
          if (value === 'system' || value === 'light' || value === 'dark') setTheme(value);
        }}
      >
        <option value="system">跟随系统</option>
        <option value="light">浅色</option>
        <option value="dark">深色</option>
      </Select>
    </Label>
  );
}
