import { useI18n } from "../lib/i18n";

type StatusBadgeProps = {
  status: string;
};

export function StatusBadge({ status }: StatusBadgeProps) {
  const { t } = useI18n();
  const normalized = status.toLowerCase().replaceAll("_", "-");
  return <span className={`status status-${normalized}`}>{t(`status.${normalized}`)}</span>;
}
