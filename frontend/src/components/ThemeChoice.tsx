import { SegmentedControl } from "@/components/ui/choice";
import { SettingRow } from "@/components/layout/PageShell";
import { THEME_OPTIONS, normalizeThemePreset, type ThemePreset } from "@/lib/theme";

/** ThemeChoice keeps the four presets in the same compact control rhythm as
 *  the other appearance settings; the live UI is the theme preview. */
export function ThemeChoice({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: ThemePreset) => void;
}) {
  const current = normalizeThemePreset(value);
  const description = THEME_OPTIONS.find((option) => option.value === current)?.description;
  return (
    <SettingRow label="主题" description={description}>
      <SegmentedControl
        ariaLabel="主题"
        value={current}
        options={THEME_OPTIONS}
        onChange={onChange}
      />
    </SettingRow>
  );
}
