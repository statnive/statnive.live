import { Overview } from './panels/Overview';
import './App.css';

export function App() {
  return (
    <main>
      <header class="statnive-header">
        <h1 class="statnive-wordmark">
          statnive<em class="statnive-wordmark-live">.live</em>
        </h1>
      </header>
      <Overview />
    </main>
  );
}
