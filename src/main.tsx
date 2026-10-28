import React from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import './styles.css';
import './markdown-editor/rich-markdown-editor.css';

const wailsRuntime = '/wails/runtime.js';
void import(/* @vite-ignore */ wailsRuntime).catch(() => null);

createRoot(document.getElementById('root')!).render(<React.StrictMode><App /></React.StrictMode>);
