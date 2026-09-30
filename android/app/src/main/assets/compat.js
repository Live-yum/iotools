'use strict';
if(typeof globalThis==='undefined')window.globalThis=window;
if(typeof window.queueMicrotask!=='function')window.queueMicrotask=function(task){Promise.resolve().then(task).catch(function(error){setTimeout(function(){throw error;},0);});};
window.terminalLoadError='';
window.addEventListener('error',function(event){window.terminalLoadError=String(event.message||'script error');});
