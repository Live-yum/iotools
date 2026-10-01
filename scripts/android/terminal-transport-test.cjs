const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict'),path=require('node:path');
let terminal,observer,dimensions={cols:80,rows:24};const events=[],timers=[];
class Terminal {constructor(){terminal=this;this.cols=80;this.rows=24;}loadAddon(){}open(){}onData(){}resize(c,r){events.push(['resize',c,r]);this.cols=c;this.rows=r;}write(data,done){this.data=Buffer.from(data);this.done=done;}paste(){}focus(){}}
class Fit {proposeDimensions(){return dimensions;}}
const context={Terminal,FitAddon:{FitAddon:Fit},Uint8Array,TextEncoder,Number,document:{getElementById(){return{}}},atob:s=>Buffer.from(s,'base64').toString('binary'),setTimeout:f=>timers.push(f),ResizeObserver:class{constructor(f){observer=f;}observe(){}},IOTools:{ready:(...args)=>events.push(['ready',...args]),resize:(...args)=>events.push(['nativeResize',...args]),outputDone:id=>events.push(['ack',id])}};
context.window=context;vm.createContext(context);vm.runInContext(fs.readFileSync(path.join(__dirname,'../../android/app/src/main/assets/terminal.js'),'utf8'),context);
timers.shift()();assert.deepEqual(events,[['ready',80,24]]);events.length=0;
context.receiveTerminal(Buffer.from('客服😀').toString('base64'),7);assert.equal(terminal.data.toString(),'客服😀');
dimensions={cols:100,rows:30};observer();dimensions={cols:90,rows:40};observer();assert.equal(events.length,0,'resize must wait until parser completion');
assert.throws(()=>context.receiveTerminal('QQ==',8),/completion acknowledgement/);terminal.done();assert.deepEqual(events,[['resize',90,40],['nativeResize',90,40],['ack',7]]);
events.length=0;context.receiveTerminal('Qg==',8);terminal.done();assert.deepEqual(events,[['ack',8]]);
dimensions={cols:NaN,rows:NaN};observer();assert.equal(terminal.cols,90);assert.equal(terminal.rows,40);
console.log('PASS terminal UTF-8 transport, completion acknowledgement, deferred/coalesced resize, invalid size fallback');
