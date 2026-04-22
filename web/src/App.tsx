import { Overview } from './panels/Overview';
import { Nav } from './components/Nav';
import { DatePicker } from './components/DatePicker';
import { FilterPanel } from './components/FilterPanel';
import { LazyPanel } from './components/LazyPanel';
import { SiteSwitcher } from './components/SiteSwitcher';
import { hashSignal } from './state/hash';
import './App.css';

// Only Overview is statically imported — every other panel ships in its
// own chunk via LazyPanel per `bundle-dynamic-imports`. Keeps initial
// JS small (Overview is the default landing panel, so no waterfall) and
// caps any single panel's weight against the overall 13 KB gz budget.
function renderPanel() {
  switch (hashSignal.value.panel) {
    case 'overview':
      return <Overview />;
    case 'sources':
      return <LazyPanel name="sources" />;
    case 'pages':
      return <LazyPanel name="pages" />;
    case 'seo':
      return <LazyPanel name="seo" />;
    case 'campaigns':
      return <LazyPanel name="campaigns" />;
    case 'realtime':
      return <LazyPanel name="realtime" />;
    default:
      return <Overview />;
  }
}

export function App() {
  return (
    <main>
      <header class="statnive-header">
        <h1 class="statnive-wordmark">
          statnive<em class="statnive-wordmark-live">.live</em>
        </h1>
        <SiteSwitcher />
      </header>
      <Nav />
      <DatePicker />
      <FilterPanel />
      {renderPanel()}
    </main>
  );
}
