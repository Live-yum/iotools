'use strict';
if(typeof globalThis==='undefined')window.globalThis=window;
if(typeof window.queueMicrotask!=='function')window.queueMicrotask=function(task){Promise.resolve().then(task).catch(function(error){setTimeout(function(){throw error;},0);});};
window.terminalLoadError='';window.terminalFaultState='';
window.addEventListener('error',function(event){if(!window.terminalFaultState&&typeof window.terminalFaultSnapshot==='function'){try{window.terminalFaultState=JSON.stringify(window.terminalFaultSnapshot());}catch(ignored){}};window.terminalLoadError=(window.terminalLoadError+'\n'+String(event.message||'script error')+' '+String(event.error&&event.error.stack||'')).slice(0,8192);});
// ParentNode.replaceChildren (Chrome 86+) is used by the bundled DOM renderer.
// Implement only this missing operation with native node/text APIs.
[Element.prototype,Document.prototype,DocumentFragment.prototype].forEach(function(prototype){
 if(typeof prototype.replaceChildren==='function')return;
 Object.defineProperty(prototype,'replaceChildren',{configurable:true,writable:true,value:function(){
  var document=this.ownerDocument||this,fragment=document.createDocumentFragment();
  for(var i=0;i<arguments.length;i++){var node=arguments[i];fragment.appendChild(node instanceof Node?node:document.createTextNode(String(node)));}
  while(this.firstChild)this.removeChild(this.firstChild);
  this.appendChild(fragment);
 }});
});
