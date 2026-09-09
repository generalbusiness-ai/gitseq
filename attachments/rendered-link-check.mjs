import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
const uiRoot=resolve('ui');
const require=createRequire(resolve(uiRoot,'package.json'));
const {JSDOM}=require('jsdom');
const dom=new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>',{pretendToBeVisual:true,url:'http://127.0.0.1:7777/'});
Object.assign(globalThis,{window:dom.window,document:dom.window.document,HTMLElement:dom.window.HTMLElement,Element:dom.window.Element,Node:dom.window.Node,MouseEvent:dom.window.MouseEvent,IS_REACT_ACT_ENVIRONMENT:true});
Object.defineProperty(globalThis,'navigator',{value:dom.window.navigator,configurable:true});
const React=require('react');const {act}=React;const {createRoot}=require('react-dom/client');
const {createServer}=await import(pathToFileURL(require.resolve('vite')));
const head=execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim();
const event=process.argv[2] || 'git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3c37468fa057f5e5b41c51c0f502c94cab51a04f';
const path='notes/2026-09-06-staleness-plan-recovery.md';
const text=execFileSync('git',['show',head+':'+path],{encoding:'utf8'});
const expected=JSON.parse(readFileSync('/tmp/git22329-source-links.json','utf8')).links;
const opened=[];const vite=await createServer({root:uiRoot,server:{middlewareMode:true},appType:'custom'});const mounted=createRoot(document.getElementById('root'));
try {
 const {MarkdownPreview,PreviewContext}=await vite.ssrLoadModule('/src/components/Preview.tsx');
 await act(async()=>mounted.render(React.createElement(PreviewContext.Provider,{value:{address:{kind:'thread',event},open(target){opened.push(target)}}},React.createElement(MarkdownPreview,{text,event,commit:head,path}))));
 assert.ok(!document.body.textContent.includes('(link unavailable)'),'rendered an unsupported link');
 const links=[...document.querySelectorAll('a')];
 const external=links.filter(a=>a.getAttribute('href').startsWith('https://github.com/'));
 assert.equal(external.length,expected.length,'ordinary source hyperlink count');
 for(const e of expected){
  const token=e.path+':'+e.line;const link=links.find(a=>a.textContent===token);assert.ok(link,'missing literal preview '+token);
  await act(async()=>link.dispatchEvent(new dom.window.MouseEvent('click',{bubbles:true,cancelable:true,button:0})));
  assert.deepEqual(opened.at(-1),{event,path:e.path,commit:head,line:e.line});
  assert.ok(external.some(a=>a.href===`https://github.com/generalbusiness-ai/gitseq/blob/${e.commit}/${e.path}#L${e.line}`),'external source mismatch '+token);
 }
 const result={head,event,ordinary_links:external.length,clicked_previews:opened.length,targets:opened,method:'Actual MarkdownPreview/ReferenceText rendered and clicked in JSDOM using existing Vite loader. No target normalization or fake preview endpoint. This check verifies renderer dispatch, not resident admission or a native browser.'};
 writeFileSync('/tmp/git22329-rendered-links.json',JSON.stringify(result,null,2));writeFileSync('/tmp/git22329-rendered-note.html',document.body.innerHTML);
 console.log('PASS: '+external.length+' ordinary exact source hyperlinks and '+opened.length+' clicked Gitseq line references; no unavailable links');
} finally {await act(async()=>mounted.unmount());await vite.close();dom.window.close();}
