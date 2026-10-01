// DOM/encoding regression only; actual visual acceptance runs on Android.
const fs=require('fs'),path=require('path'),assert=require('assert');
const root=path.resolve(__dirname,'../..');
const {JSDOM}=require(process.env.IOTOOLS_JSDOM_MODULE||path.join(root,'android/node_modules/jsdom'));
const {TextDecoder}=require('util');
const fixture=Buffer.from(fs.readFileSync(path.join(__dirname,'testdata/native-startup.base64'),'utf8').replace(/\s/g,''),'base64');
async function replay(strings){
 const w=new JSDOM('<div id="terminal"></div>',{runScripts:'outside-only',pretendToBeVisual:true}).window;
 w.matchMedia=()=>({matches:false,addListener(){},removeListener(){}});
 w.HTMLCanvasElement.prototype.getContext=()=>({measureText:()=>({width:9}),createLinearGradient(){return{addColorStop(){}}},getImageData(){return{data:[0,0,0,255]}},fillRect(){}});
 for(const p of [w.Element.prototype,w.Document.prototype,w.DocumentFragment.prototype])delete p.replaceChildren;
 for(const file of ['compat.js','vendor/xterm.js'])w.eval(fs.readFileSync(path.join(root,'android/app/src/main/assets',file),'utf8'));
 const term=new w.Terminal({cols:81,rows:41});term.open(w.document.getElementById('terminal'));
 const decoder=new TextDecoder('utf-8',{ignoreBOM:true});
 // Every byte boundary is exercised in Chinese/emoji, and native frames split at 137 bytes.
 const input=Buffer.concat([fixture,Buffer.from('\x1b[2;1H\x1b[4;38;2;10;20;30m甲😀乙\x1b[0m')]);
 for(let n=0;n<input.length;n+=137){const bytes=new w.Uint8Array(input.slice(n,n+137));await new Promise(done=>term.write(strings?decoder.decode(bytes,{stream:true}):bytes,done));}
 await new Promise(done=>setTimeout(done,50));assert.equal(w.terminalLoadError,'');
 assert(w.document.querySelector('.xterm-rows').textContent.includes('客服验收'),'customer text must render in DOM');
 const state={x:term.buffer.active.cursorX,y:term.buffer.active.cursorY,cells:[]};
 for(let y=0;y<term.rows;y++)for(let x=0;x<term.cols;x++){const c=term.buffer.active.getLine(y).getCell(x);state.cells.push([c.getChars(),c.getWidth(),c.getFgColorMode(),c.getFgColor(),c.getBgColorMode(),c.getBgColor(),c.isBold(),c.isItalic(),c.isUnderline()]);}
 term.dispose();w.close();return state;
}
(async()=>{assert.deepStrictEqual(await replay(false),await replay(true));console.log('PASS actual Android ANSI fixture byte/string paths: rendered Chinese, emoji, colors, underline, cursor and cell attributes');})().catch(error=>{console.error(error);process.exit(1);});
