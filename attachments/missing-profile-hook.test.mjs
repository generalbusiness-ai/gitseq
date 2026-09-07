import test from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { createServer } from 'vite';

async function fixture(body, options={}) {
 const retainedProfile=Object.hasOwn(options,'profile')?options.profile:'app@fold-1';
 const dom=new JSDOM('<div id="root"></div>',{url:'http://localhost/',pretendToBeVisual:true});
 globalThis.window=dom.window;globalThis.document=dom.window.document;globalThis.IS_REACT_ACT_ENVIRONMENT=true;
 const vite=await createServer({root:fileURLToPath(new URL('..',import.meta.url)),appType:'custom',logLevel:'silent',server:{middlewareMode:true}});
 const {useWorkroom}=await vite.ssrLoadModule('/src/lib/store.ts');
 const {rebuildQualifier}=await vite.ssrLoadModule('/src/lib/rebuild.ts');
 const nativeInterval=globalThis.setInterval,nativeClear=globalThis.clearInterval,nativeFetch=globalThis.fetch;
 const timers=new Map(),waits=[],rebuilds=[],activeRebuilds=new Set();let latest;
 const status=(head,depth)=>({profile:retainedProfile,durable:{head,depth,projection:{actors:{}}},cursor:{frontier:[{head,depth}],live:{generation:'g',position:depth}}});
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


for(const running of [false,true]) {
 for(const [name,retained,reported,safe] of [
  ['same known profile','app@fold-1','app@fold-1',true],
  ['different known profile','app@fold-1','app@fold-2',false],
  ['old retained status has no profile',undefined,'app@fold-2',false],
  ['probe has no profile','app@fold-1',undefined,false],
  ['probe declares empty profile','app@fold-1','',false],
  ['neither side names a profile',undefined,undefined,false]
 ]) {
  test(name+' running='+running,async()=>fixture(async f=>{
   const before=f.get().status;
   await act(async()=>[...f.timers.values()][0]());
   const answer={running,verified:1,total:10};if(reported!==undefined)answer.profile=reported;
   await act(async()=>f.rebuilds[0](f.reply(answer)));
   console.log(name,running,'status retained',f.get().status===before,'qualifier',document.body.textContent);
   if(safe) assert.equal(f.get().status,before,'a demonstrably same-profile snapshot remains available');
   else assert.equal(f.get().status,undefined,'unestablished or different interpretation must not retain the projection');
  },{profile:retained}));
 }
}
