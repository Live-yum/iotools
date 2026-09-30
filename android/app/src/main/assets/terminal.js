'use strict';
const term=new Terminal({fontSize:13,fontFamily:'monospace',cursorBlink:true,scrollback:1000,convertEol:false,allowProposedApi:false,theme:{background:'#000000',foreground:'#eeeeee'}});
const fit=new FitAddon.FitAddon();
term.loadAddon(fit);term.open(document.getElementById('terminal'));
let started=false;
function resize(){fit.fit();const cols=Math.max(20,Math.min(300,term.cols)),rows=Math.max(8,Math.min(150,term.rows));term.resize(cols,rows);if(!started){started=true;IOTools.ready(cols,rows);}else IOTools.resize(cols,rows);}
term.onData(data=>IOTools.input(data));
window.receiveTerminal=function(encoded){const raw=atob(encoded),data=new Uint8Array(raw.length);for(let i=0;i<raw.length;i++)data[i]=raw.charCodeAt(i);term.write(data);};
window.terminalPaste=function(text){if(new TextEncoder().encode(text).length<=65536)term.paste(text);};
window.terminalFocus=function(){term.focus();};
window.terminalText=function(){let text='';for(let i=0;i<term.buffer.active.length;i++){const line=term.buffer.active.getLine(i);if(line)text+=line.translateToString(true)+'\n';}return text;};
if(typeof ResizeObserver==='function')new ResizeObserver(()=>resize()).observe(document.getElementById('terminal'));
else window.addEventListener('resize',resize);
setTimeout(resize,50);
