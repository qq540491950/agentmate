import { SettingRow } from "@/components/layout/PageShell";
import { SegmentedControl } from "@/components/ui/choice";

export type PolicyChoice<T extends string> = {
  value: T;
  label: string;
  description: string;
  danger?: boolean;
};

export function PolicyChoiceGroup<T extends string>({
  label,
  description,
  options,
  value,
  onSelect,
}: {
  label: string;
  description: string;
  options: readonly PolicyChoice<T>[];
  value: T | null;
  onSelect: (value: T) => void;
}) {
  const selected = options.find((option) => option.value === value);

  return (
    <SettingRow label={label} description={selected?.description ?? description}>
      <SegmentedControl
        ariaLabel={label}
        value={value}
        options={options}
        onChange={onSelect}
      />
    </SettingRow>
  );
}
