import test from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { createServer } from 'vite';

async function fixture(body) {
 const dom=new JSDOM('<div id="root"></div>',{url:'http://localhost/',pretendToBeVisual:true});
 globalThis.window=dom.window;globalThis.document=dom.window.document;globalThis.IS_REACT_ACT_ENVIRONMENT=true;
 const vite=await createServer({root:fileURLToPath(new URL('..',import.meta.url)),appType:'custom',logLevel:'silent',server:{middlewareMode:true}});
 const {useWorkroom}=await vite.ssrLoadModule('/src/lib/store.ts');
 const {rebuildQualifier}=await vite.ssrLoadModule('/src/lib/rebuild.ts');
 const nativeInterval=globalThis.setInterval,nativeClear=globalThis.clearInterval,nativeFetch=globalThis.fetch;
 const timers=new Map(),waits=[],rebuilds=[],activeRebuilds=new Set();let latest;
 const status=(head,depth)=>({durable:{head,depth,projection:{actors:{}}},cursor:{frontier:[{head,depth}],live:{generation:'g',position:depth}}});
 const reply=value=>({ok:true,json:async()=>value});
 globalThis.setInterval=(fn,ms,...args)=>{if(ms!==1000)return nativeInterval(fn,ms,...args);const id={unref(){}};timers.set(id,fn);return id};
 globalThis.clearInterval=id=>{if(!timers.delete(id))nativeClear(id)};
 globalThis.fetch=(url,options={})=>{
  if(url==='/v0/status')return Promise.resolve(reply(status('old00000',1)));
  if(url==='/v0/actors')return Promise.resolve(reply([]));
  if(url==='/v0/worktrees')return Promise.resolve(reply({repo:'',worktrees:[]}));
  if(url==='/v0/wait')return new Promise(resolve=>waits.push(resolve));
  if(url==='/v0/rebuild')return new Promise((resolve,reject)=>{const token={};activeRebuilds.add(token);rebuilds.push(value=>{activeRebuilds.delete(token);resolve(value)});options.signal?.addEventListener('abort',()=>{activeRebuilds.delete(token);reject(new Error('aborted'))},{once:true})});
  throw new Error('unexpected fetch '+url);
 };
 function View(){latest=useWorkroom();return React.createElement('div',{},rebuildQualifier(latest.status,latest.rebuilding)??'current')}
 const root=createRoot(document.getElementById('root'));
 try {
  await act(async()=>root.render(React.createElement(View)));
  assert.equal(waits.length,1,'actual hook must reach its first long poll');assert.equal(timers.size,1,'actual hook must start its probe interval');
  await body({timers,waits,rebuilds,activeRebuilds,status,reply,get:()=>latest});
 } finally {await act(async()=>root.unmount());globalThis.setInterval=nativeInterval;globalThis.clearInterval=nativeClear;globalThis.fetch=nativeFetch;await vite.close();dom.window.close()}
}

test('a rebuild reply from an old wait cannot relabel a newer status',async()=>fixture(async f=>{
 await act(async()=>[...f.timers.values()][0]());assert.equal(f.rebuilds.length,1);
 await act(async()=>f.waits[0](f.reply({status:f.status('new00000',2)})));
 assert.equal(f.get().status.durable.head,'new00000');assert.equal(f.get().rebuilding,undefined);assert.equal(f.waits.length,2);
 await act(async()=>f.rebuilds[0](f.reply({running:true,verified:1,total:10})));
 console.log('late reply after newer status:',f.get().status.durable.head,f.get().rebuilding,document.body.textContent);
 assert.equal(f.get().rebuilding,undefined,'old wait reply must not revive rebuilding on the new frontier');
}));

test('a slow rebuild endpoint cannot accumulate overlapping probes',async()=>fixture(async f=>{
 const tick=[...f.timers.values()][0];await act(async()=>{tick();tick();tick()});
 console.log('unresolved actual /v0/rebuild fetches:',f.rebuilds.length);
 assert.equal(f.rebuilds.length,1,'only one rebuild request may be in flight per wait');
}));


test('pending probes cannot accumulate across successive waits',async()=>fixture(async f=>{
 for(let i=0;i<3;i++) {
  await act(async()=>[...f.timers.values()][0]());
  await act(async()=>f.waits[i](f.reply({status:f.status('new'+i,2+i)})));
  assert.equal(f.waits.length,i+2,'actual wait loop must advance');
 }
 console.log('unresolved /v0/rebuild fetches across three completed waits:',f.activeRebuilds.size);
 assert.ok(f.activeRebuilds.size<=1,'each completed wait must cancel its request or preserve one shared in-flight bound');
}));
