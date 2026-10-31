import '@overfin/ui/theme.css';
import '@overfin/ui/admin.css';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';

mount(App, { target: document.getElementById('app')! });
