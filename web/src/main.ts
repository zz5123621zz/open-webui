import './app.css';
import './redesign.css';
import App from './App.svelte';
import { mount } from 'svelte';

const savedFontSize = localStorage.getItem('personal-chat-font-size');
if (savedFontSize === 'compact' || savedFontSize === 'standard' || savedFontSize === 'large') {
  document.documentElement.dataset.fontSize = savedFontSize;
}

mount(App, {
  target: document.getElementById('app')!
});
