const fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../..');
const esbuild=require(path.join(root,'android/node_modules/esbuild'));
const destination=path.join(root,'android/app/src/main/assets/vendor');
fs.mkdirSync(destination,{recursive:true});
let source=fs.readFileSync(path.join(root,'android/node_modules/@xterm/xterm/lib/xterm.js'),'utf8');
// Unicode General_Category=Control is exactly these ranges. Avoid a newer
// RegExp syntax in Android 8's system WebView, while preserving its meaning.
const slash=String.fromCharCode(92);
const needle='/'+slash+'p{Control}/u';
const replacement='/['+slash+'x00-'+slash+'x1f'+slash+'x7f-'+slash+'x9f]/u';
if(source.split(needle).length!==2)throw new Error('Pinned xterm Control expression changed; review compatibility transform');
source=source.replace(needle,replacement);
fs.writeFileSync(path.join(destination,'xterm.js'),esbuild.transformSync(source,{loader:'js',target:'chrome61',minify:true,legalComments:'inline'}).code);
const fit=fs.readFileSync(path.join(root,'android/node_modules/@xterm/addon-fit/lib/addon-fit.js'),'utf8');
fs.writeFileSync(path.join(destination,'addon-fit.js'),esbuild.transformSync(fit,{loader:'js',target:'chrome61',minify:true,legalComments:'inline'}).code);
