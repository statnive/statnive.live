import { render } from 'preact';
import './tokens.css';
import './reset.css';
import { App } from './App';

const root = document.getElementById('statnive-app');
if (root) {
  render(<App />, root);
}
