import { TaskBoard } from "../features/tasks/TaskBoard";
import { I18nProvider } from "../lib/i18n";

export function App() {
  return (
    <I18nProvider>
      <TaskBoard />
    </I18nProvider>
  );
}
