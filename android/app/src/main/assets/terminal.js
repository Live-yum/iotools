'use strict';
const term=new Terminal({fontSize:13,fontFamily:'monospace',cursorBlink:true,scrollback:1000,convertEol:false,allowProposedApi:false,theme:{background:'#000000',foreground:'#eeeeee'}});
const fit=new FitAddon.FitAddon();
term.loadAddon(fit);term.open(document.getElementById('terminal'));
let started=false,writing=false,pendingSize=null;
let outputDecoder=new TextDecoder('utf-8',{fatal:false,ignoreBOM:true}),decoderGeneration=null;
// Bounded numeric-only fault snapshot; no request/response text is collected.
window.terminalFaultSnapshot=function(){
 const buffer=term._core._bufferService.buffer,invalid=[];
 for(let y=0;y<buffer.lines.length&&invalid.length<8;y++){const line=buffer.lines.get(y);for(let x=0;x<line.length&&invalid.length<8;x++){const bg=line._data[x*3+2];if((bg&0x10000000)&&!line._extendedAttrs[x])invalid.push({x:x,y:y,bg:bg,fg:line._data[x*3+1],attrs:Object.keys(line._extendedAttrs).length});}}
 const renderer=term._core._renderService._renderer.value,cell=renderer&&renderer._rowFactory&&renderer._rowFactory._workCell;
 return {cols:term.cols,rows:term.rows,invalid:invalid,work:cell?{bg:cell.bg,fg:cell.fg,extended:typeof cell.extended}:null};
};
function applyResize(){
 if(writing||!pendingSize)return;
 const size=pendingSize;pendingSize=null;
 const changed=term.cols!==size.cols||term.rows!==size.rows;
 if(changed)term.resize(size.cols,size.rows);
 if(!started){started=true;IOTools.ready(size.cols,size.rows);}
 else if(changed)IOTools.resize(size.cols,size.rows);
}
function resize(){const proposed=fit.proposeDimensions();const size=proposed&&Number.isFinite(proposed.cols)&&Number.isFinite(proposed.rows)?proposed:{cols:term.cols,rows:term.rows};pendingSize={cols:Math.max(20,Math.min(300,size.cols)),rows:Math.max(8,Math.min(150,size.rows))};applyResize();}
term.onData(data=>IOTools.input(data));
window.receiveTerminal=function(encoded,sequence,generation){
 if(writing)throw new Error('Terminal output requires completion acknowledgement');
 if(generation!==decoderGeneration){outputDecoder=new TextDecoder('utf-8',{fatal:false,ignoreBOM:true});decoderGeneration=generation;}
 const raw=atob(encoded),data=new Uint8Array(raw.length);for(let i=0;i<raw.length;i++)data[i]=raw.charCodeAt(i);
 writing=true;
 // Use the documented string path; preserve UTF-8 split across native frames.
 term.write(outputDecoder.decode(data,{stream:true}),function(){writing=false;try{applyResize();}finally{IOTools.outputDone(sequence);}});
};
window.terminalPaste=function(text){if(new TextEncoder().encode(text).length<=65536)term.paste(text);};
window.terminalFocus=function(){term.focus();};
window.terminalText=function(){let text='';for(let i=0;i<term.buffer.active.length;i++){const line=term.buffer.active.getLine(i);if(line)text+=line.translateToString(true)+'\n';}return text;};
if(typeof ResizeObserver==='function')new ResizeObserver(()=>resize()).observe(document.getElementById('terminal'));
else window.addEventListener('resize',resize);
setTimeout(resize,50);
