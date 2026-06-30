import { useI18n } from "../lib/i18n";

type StatusBadgeProps = {
  status: string;
};

export function StatusBadge({ status }: StatusBadgeProps) {
  const { t } = useI18n();
  const normalized = status.toLowerCase().replaceAll("_", "-");
  const key = `status.${normalized}`;
  const label = t(key);
  return <span className={`status status-${normalized}`}>{label === key ? labelize(status) : label}</span>;
}

function labelize(value: string) {
  return value
    .replaceAll("_", " ")
    .replaceAll("-", " ")
    .replace(/\b\w/g, (match) => match.toUpperCase());
}
