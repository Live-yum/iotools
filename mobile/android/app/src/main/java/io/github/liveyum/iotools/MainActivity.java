package io.github.liveyum.iotools;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import io.flutter.embedding.android.FlutterActivity;
import io.flutter.embedding.engine.FlutterEngine;
import io.flutter.plugin.common.MethodCall;
import io.flutter.plugin.common.MethodChannel;
import org.json.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;

/** Thin operating-system bridge. No protocol UI or terminal renderer lives here. */
public final class MainActivity extends FlutterActivity {
 private final ExecutorService engineIO=Executors.newFixedThreadPool(3), fileIO=Executors.newSingleThreadExecutor();
 private final Handler main=new Handler(Looper.getMainLooper());
 private volatile boolean destroyed, opened;
 private volatile MobileFiles.Cancel transfer;
 private MethodChannel.Result picker;
 private String pickerKind; private Map<String,Object> pickerArgs;
 private static final int PICK=501;
 @Override public void configureFlutterEngine(FlutterEngine engine){
  super.configureFlutterEngine(engine);
  UsbSerialTransport.initialize(this,status->{});
  new MethodChannel(engine.getDartExecutor().getBinaryMessenger(),"io.github.liveyum.iotools/engine").setMethodCallHandler(this::engineCall);
  new MethodChannel(engine.getDartExecutor().getBinaryMessenger(),"io.github.liveyum.iotools/platform").setMethodCallHandler(this::platformCall);
 }
 private interface Work { Object run() throws Exception; }
 private void work(ExecutorService pool,MethodChannel.Result result,Work operation){pool.execute(()->{try{Object value=operation.run();main.post(()->{if(!destroyed)result.success(value);});}catch(Exception error){main.post(()->{if(!destroyed)result.error("iotools",error.getMessage()==null?"本地操作失败":error.getMessage(),null);});}});}
 private void engineCall(MethodCall call,MethodChannel.Result result){
  if(call.method.equals("pause")){NativeRuntime.pause();UsbSerialTransport.pause();result.success(null);return;}
  if(call.method.equals("resume")){NativeRuntime.resume();UsbSerialTransport.resume();result.success(null);return;}
  if(call.method.equals("close")){NativeRuntime.close();UsbSerialTransport.close();opened=false;result.success(null);return;}
  if(call.method.equals("open")){work(engineIO,result,()->{String path=call.argument("path");File file=MobileFiles.privateFile(getFilesDir(),new File(path==null?"iotools.yaml":path),false);String response=NativeRuntime.openInRoot(file.getAbsolutePath(),getFilesDir().getCanonicalPath(),BuildConfig.GIT_SHA,Boolean.TRUE.equals(call.argument("readOnly")),Boolean.TRUE.equals(call.argument("history")),path!=null);opened=true;return response;});return;}
  if(call.method.equals("command")){String json=call.argument("json");if(json==null||json.length()>8*1024*1024){result.error("invalid","命令缺失或超过上限",null);return;}boolean trace=false;if(BuildConfig.DEBUG){try{trace="preview".equals(new JSONObject(json).optString("op"));}catch(JSONException ignored){}}final boolean previewTrace=trace;final long queued=android.os.SystemClock.elapsedRealtime();if(previewTrace)android.util.Log.d("IotoolsPreview","queued local preview");work(engineIO,result,()->{if(previewTrace)android.util.Log.d("IotoolsPreview","native-start queue_ms="+(android.os.SystemClock.elapsedRealtime()-queued));String reply=NativeRuntime.command(json);if(previewTrace)android.util.Log.d("IotoolsPreview","native-end total_ms="+(android.os.SystemClock.elapsedRealtime()-queued));return reply;});return;}
  result.notImplemented();
 }
 @SuppressWarnings("unchecked") private void platformCall(MethodCall call,MethodChannel.Result result){
  Map<String,Object> args=call.arguments instanceof Map?(Map<String,Object>)call.arguments:new HashMap<>();
  if(call.method.equals("files.cancel")){MobileFiles.Cancel active=transfer;if(active!=null){active.signal();engineIO.execute(active::cancel);}result.success(null);return;}
  if(call.method.equals("files.pick")||call.method.equals("files.importBundle")||call.method.equals("files.export")){
   if(picker!=null||transfer!=null){result.error("busy","已有文件选择或传输正在进行",null);return;}
   try{long limit=number(args,"limit",MobileFiles.DEFAULT_LIMIT);MobileFiles.checkLimit(limit);boolean exporting=call.method.equals("files.export");if(exporting&&args.get("path")!=null){File source=privatePath(args,true);if(source.length()>limit)throw new IOException("文件超过导出上限，未创建目标");args.put("sourceSize",source.length());args.put("sourceModified",source.lastModified());}
    Intent intent=new Intent(exporting?Intent.ACTION_CREATE_DOCUMENT:Intent.ACTION_OPEN_DOCUMENT).setType(call.method.equals("files.importBundle")?"application/zip":"*/*").addCategory(Intent.CATEGORY_OPENABLE);
    intent.addFlags(exporting?Intent.FLAG_GRANT_WRITE_URI_PERMISSION:Intent.FLAG_GRANT_READ_URI_PERMISSION);
    if(exporting)intent.putExtra(Intent.EXTRA_TITLE,string(args,"name","iotools-export.txt"));
    picker=result;pickerKind=call.method;pickerArgs=new HashMap<>(args);startActivityForResult(intent,PICK);
   }catch(Exception error){picker=null;pickerArgs=null;result.error("files",error.getMessage(),null);}return;
  }
  if(call.method.equals("settings.get")){String privateRoot;try{privateRoot=getFilesDir().getCanonicalPath();}catch(IOException error){result.error("files","无法解析应用私有目录",null);return;}Map<String,Object> values=new HashMap<>();values.put("root",privateRoot);values.put("android_api",android.os.Build.VERSION.SDK_INT);values.put("platform","android");values.put("version",BuildConfig.VERSION_NAME);values.put("sha",BuildConfig.GIT_SHA);values.put("usb",true);android.content.SharedPreferences prefs=getSharedPreferences("flutter-settings",0);values.put("theme",prefs.getString("theme","dark"));values.put("readOnly",prefs.getBoolean("readOnly",false));values.put("history",prefs.getBoolean("history",true));values.put("collection",prefs.getString("collection","iotools.yaml"));result.success(values);return;}
  work(call.method.startsWith("files.")?fileIO:engineIO,result,()->{
   switch(call.method){
    case "settings.save":{
     validateSettings(getFilesDir(),args);
     android.content.SharedPreferences.Editor edit=getSharedPreferences("flutter-settings",0).edit();
     for(Map.Entry<String,Object> entry:args.entrySet())if(entry.getValue() instanceof Boolean)edit.putBoolean(entry.getKey(),(Boolean)entry.getValue());else edit.putString(entry.getKey(),entry.getValue().toString());
     if(!edit.commit())throw new IOException("无法保存应用设置");return true;
    }
    case "help.read":return readAsset("android.md",2L<<20);
    case "licenses.read":return readAsset("LICENSE",2L<<20);
    case "files.list":{ArrayList<Map<String,Object>> files=new ArrayList<>();listFiles(getFilesDir(),files,0);return files;}
    case "files.read":{File file=privatePath(args,true);long limit=Math.min(number(args,"limit",8L<<20),8L<<20);if(file.length()>limit)throw new IOException("文本文件超过编辑上限");try(InputStream input=new FileInputStream(file);ByteArrayOutputStream output=new ByteArrayOutputStream()){MobileFiles.copyBounded(input,output,limit,file.length(),new MobileFiles.Cancel());return new String(output.toByteArray(),StandardCharsets.UTF_8);}}
    case "files.write":{throw new IOException("配置请通过引擎 config.save 原子保存，避免绕过校验");}
    case "usb.list":return UsbSerialTransport.listPorts().toString();
    case "usb.permission":return UsbSerialTransport.requestPermission(string(args,"endpoint",""));
    case "usb.open":UsbSerialTransport.open(string(args,"endpoint",""),(int)number(args,"baud",9600),(int)number(args,"dataBits",8),(int)number(args,"stopBits",1),string(args,"parity","N"));return UsbSerialTransport.status();
    case "usb.close":UsbSerialTransport.close();return UsbSerialTransport.status();
    case "usb.status":return UsbSerialTransport.status();
    default:throw new IOException("不支持的平台操作："+call.method);
   }
  });
 }
 @Override protected void onActivityResult(int request,int code,Intent data){super.onActivityResult(request,code,data);if(request!=PICK)return;MethodChannel.Result result=picker;String kind=pickerKind;Map<String,Object> args=pickerArgs;picker=null;pickerArgs=null;if(result==null)return;if(code!=Activity.RESULT_OK||data==null||data.getData()==null){result.success(null);return;}Uri uri=data.getData();if(!"content".equals(uri.getScheme())){result.error("files","请选择系统文档提供程序",null);return;}MobileFiles.Cancel cancel=new MobileFiles.Cancel();transfer=cancel;
  work(fileIO,result,()->{try{long limit=number(args,"limit",MobileFiles.DEFAULT_LIMIT);
   if(kind.equals("files.pick")){File file=MobileFiles.importDocument(getContentResolver(),uri,getFilesDir(),limit,cancel);return fileInfo(file);}
   if(kind.equals("files.importBundle")){MobileFiles.DocumentInfo info=MobileFiles.documentInfo(getContentResolver(),uri);if(info.size>limit)throw new IOException("文件包超过所选上限");try(InputStream input=getContentResolver().openInputStream(uri)){if(input==null)throw new IOException("无法读取文件包");cancel.track(input);try{MobileFiles.Bundle bundle=MobileFiles.extractBundle(input,getFilesDir(),limit,info.size,cancel);Map<String,Object> out=new HashMap<>();out.put("directory",bundle.directory.getAbsolutePath());out.put("bytes",bundle.bytes);ArrayList<Map<String,Object>> files=new ArrayList<>();for(File file:bundle.files)files.add(fileInfo(file));out.put("files",files);return out;}finally{cancel.release(input);}}}
   if(args.get("path")!=null){File source;try{source=privatePath(args,true);if(source.length()!=number(args,"sourceSize",-1)||source.lastModified()!=number(args,"sourceModified",-1))throw new IOException("来源已变化，请重新选择导出");}catch(IOException changed){if(!MobileFiles.deleteDocument(getContentResolver(),uri))throw new IOException(changed.getMessage()+"；无法清理新建目标，请在选择的位置移除",changed);throw changed;}MobileFiles.exportDocument(getContentResolver(),uri,source,limit,cancel);}else MobileFiles.exportTextDocument(getContentResolver(),uri,string(args,"text",""),limit,cancel);
   Map<String,Object> out=new HashMap<>();out.put("exported",true);return out;
  }finally{transfer=null;}});
 }
 private String readAsset(String name,long limit)throws IOException{try(InputStream input=getAssets().open(name);ByteArrayOutputStream output=new ByteArrayOutputStream()){MobileFiles.copyBounded(input,output,limit,-1,new MobileFiles.Cancel());return new String(output.toByteArray(),StandardCharsets.UTF_8);}}
 static void validateSettings(File root,Map<String,Object> args)throws IOException {
     Set<String> allowed=new HashSet<>(Arrays.asList("theme","readOnly","history","collection"));
     if(!allowed.containsAll(args.keySet()))throw new IOException("不能持久化未允许的设置字段");
     if(args.containsKey("theme")&&!Arrays.asList("dark","light","system").contains(args.get("theme")))throw new IOException("主题无效");
     for(String key:Arrays.asList("readOnly","history"))if(args.containsKey(key)&&!(args.get(key) instanceof Boolean))throw new IOException("布尔设置类型无效："+key);
     if(args.containsKey("collection")){String path=string(args,"collection","");if(path.isEmpty()||new File(path).isAbsolute()||path.contains(":")||path.contains("\\")||path.matches(".*[\\p{Cntrl}].*"))throw new IOException("集合必须是应用私有目录的相对路径");MobileFiles.privateFile(root,new File(path),false);for(String part:path.split("/",-1))if(part.isEmpty()||part.equals(".")||part.equals(".."))throw new IOException("集合路径包含无效目录");}
 }
 private File privatePath(Map<String,Object> args,boolean existing)throws IOException{return MobileFiles.privateFile(getFilesDir(),new File(string(args,"path","")),existing);}
 private static String string(Map<String,Object> args,String key,String fallback){Object value=args.get(key);return value==null?fallback:value.toString();}
 private static long number(Map<String,Object> args,String key,long fallback)throws IOException{try{Object value=args.get(key);return value==null?fallback:Long.parseLong(value.toString());}catch(NumberFormatException invalid){throw new IOException("需要整数字段："+key);}}
 private static Map<String,Object> fileInfo(File file)throws IOException{Map<String,Object> value=new HashMap<>();value.put("path",file.getCanonicalPath());value.put("name",file.getName());value.put("size",file.length());return value;}
 private static void listFiles(File root,List<Map<String,Object>> out,int depth)throws IOException{if(depth>8||out.size()>=512)return;File[] files=root.listFiles();if(files==null)return;Arrays.sort(files,Comparator.comparing(File::getName));for(File file:files){if(Files.isSymbolicLink(file.toPath()))continue;if(file.isDirectory())listFiles(file,out,depth+1);else if(file.isFile()&&out.size()<512)out.add(fileInfo(file));}}
 @Override protected void onStop(){super.onStop();if(opened){NativeRuntime.pause();UsbSerialTransport.pause();}}
 @Override protected void onResume(){super.onResume();if(opened){NativeRuntime.resume();UsbSerialTransport.resume();}}
 @Override protected void onDestroy(){destroyed=true;MobileFiles.Cancel active=transfer;if(active!=null){active.signal();engineIO.execute(active::cancel);}if(!isChangingConfigurations()){NativeRuntime.close();UsbSerialTransport.shutdown();}engineIO.shutdown();fileIO.shutdown();super.onDestroy();}
}
