import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const root = fileURLToPath(new URL("..", import.meta.url));
const event = `git:sha1:${"a".repeat(40)}#git:sha1:${"b".repeat(40)}`;
const head = "c".repeat(40);
const projection = {
 decisions: [{event, sequence:19016,verdict:"effective"},{event:"seed",sequence:1,verdict:"effective"}],
 statements: [{event,sequence:19016,kind:"assert",text:"Review notes",actor:"a",timestamp:123,retired:true,stale:true},{event:"request",sequence:2,kind:"request",text:"Review duty",actor:"a"}],
 acts:[],artifacts:[],commitments:[],actors:{a:{name:"Codex"}},provenance:{}
};
async function modules(run) { const vite=await createServer({root,appType:"custom",logLevel:"silent",server:{middlewareMode:true}});try{await run(vite)}finally{await vite.close()} }

test("Notes uses declared render kinds and exact lookup spans all durable events",()=>modules(async vite=>{
 const {noteRows,exactRecord}=await vite.ssrLoadModule("/src/lib/notes.ts");
 const vocabulary={definitions:[{name:"assert",render:"note"},{name:"request",render:"request"}]};
 assert.deepEqual(noteRows(projection,vocabulary,"").map(n=>n.event),[event]);
 assert.equal(noteRows(projection,vocabulary,"codex")[0].retired,true);
 assert.equal(noteRows(projection,vocabulary,"codex")[0].stale,true);
 assert.equal(noteRows(projection,undefined,"").length,0,"never guess a kind's rendering");
 assert.equal(exactRecord(projection,"#19016").event,event);
 assert.equal(exactRecord(projection,"#1").event,"seed","seed is not a statement");
 assert.equal(exactRecord(projection,"#190"),undefined);
}).catch(error=>{throw error}));

test("encoded event/focus and preview links round-trip; malformed encoding preserves valid root",()=>modules(async vite=>{
 const {parseAddress,formatAddress}=await vite.ssrLoadModule("/src/lib/address.ts");
 assert.equal(parseAddress(`#/thread/${encodeURIComponent(event)}`).event,event);
 assert.equal(parseAddress(`#/thread/${event}/${encodeURIComponent(event)}`).focus,event);
 const damaged=parseAddress(`#/thread/${encodeURIComponent(event)}/%E0%A4%A`);
 assert.equal(damaged.event,event);assert.match(damaged.error,/malformed/);
 const address={kind:"notes",preview:{event,path:"docs/a b.md",commit:head,line:12}};
 assert.deepEqual(parseAddress(formatAddress(address)),{...address,preview:{...address.preview,attachment:undefined}});
 const broken=parseAddress(`#/thread/${event}?preview_event=%XX`);
 assert.equal(broken.event,event);assert.match(broken.error,/malformed/);assert.equal(broken.preview,undefined);
}));

test("source references preserve exact paths and lines and reject unsafe URL schemes",()=>modules(async vite=>{
 const {fileReference,safeExternalLink}=await vite.ssrLoadModule("/src/lib/fileReferences.ts");
 assert.deepEqual(fileReference(`internal/service/preview.go@${head}:42`,event),{event,path:"internal/service/preview.go",commit:head,line:42});
 for(const value of ["/etc/passwd","../keys","a/../keys","javascript:alert(1)","file:///tmp/a","data:text/html,x","a\\b"]) assert.equal(fileReference(value,event),undefined,value);
 assert.equal(safeExternalLink("https://example.invalid/a"),true);
 for(const value of ["javascript:alert(1)","//example.invalid","file:///tmp/a","data:text/html,x"])assert.equal(safeExternalLink(value),false);
}));

test("Markdown remains inert text and source links use the shared in-app preview",()=>modules(async vite=>{
 const {MarkdownPreview,PreviewContext}=await vite.ssrLoadModule("/src/components/Preview.tsx");
 const html=renderToStaticMarkup(React.createElement(PreviewContext.Provider,{value:{address:{kind:"thread",event},open(){}}},React.createElement(MarkdownPreview,{event,commit:head,path:"docs/review.md",attachments:["proof.json"],text:'# Review\n<script>alert(1)</script>\n[unsafe](javascript:alert)\n![remote](https://example.invalid/track.png)\n`internal/service/preview.go:42`\n[relative](sibling.md)\n`proof.json`\n```html\n<img src=x onerror=alert(1)>\n```'})));
 assert.ok(!html.includes("<script>"));assert.ok(!html.includes("<img"));assert.ok(!html.includes('href="javascript:'));
 assert.match(html,/link unavailable/);assert.match(html,/not loaded/);assert.match(html,/line=42/);assert.match(html,/evidence=proof.json/);assert.match(html,/file=docs%2Fsibling.md/);assert.match(html,new RegExp(`at=${head}`));
}));

test("a known non-statement durable event opens readable record detail",()=>modules(async vite=>{
 const {Thread}=await vite.ssrLoadModule("/src/components/Thread.tsx");
 const {buildRecordIndex}=await vite.ssrLoadModule("/src/lib/records.ts");
 const html=renderToStaticMarkup(React.createElement(Thread,{workroom:{actors:[],status:{durable:{projection}}},session:{},frames:[],root:"seed",index:buildRecordIndex(projection),pending:[],onBack(){},onOpenThread(){},onSay(){},onSayFailed(){},doAct(){}}));
 assert.match(html,/#1 · Durable record/);assert.match(html,/effective/);assert.doesNotMatch(html,/Gone|not in the projection/);
}));
