package io.github.liveyum.iotools;

import android.app.*;
import android.os.*;
import android.content.*;
import android.net.Uri;
import android.graphics.Color;
import android.view.*;
import android.view.inputmethod.InputMethodManager;
import android.webkit.*;
import android.widget.*;
import androidx.webkit.WebViewAssetLoader;
import java.io.*;
import java.nio.charset.StandardCharsets;
import org.json.JSONObject;

public class MainActivity extends Activity {
 private WebView web;
 private TextView status;
 private final Handler handler=new Handler(Looper.getMainLooper());
 private boolean active,ready,wanted=true,starting,seenRunning,stopping;
 private int cols=80,rows=24;
 private boolean outputPending;
 private int outputSequence;
 private static final int IMPORT=21,EXPORT=22,IMPORT_FILE=23,EXPORT_FILE=24;
 private File exportSource;
 private File config;
 private java.util.function.Consumer<byte[]> outputObserver;
 void observeOutputForTest(java.util.function.Consumer<byte[]> observer){outputObserver=observer;}
 private final Runnable poll=new Runnable(){public void run(){
  if(!active)return;
  if(!ready){handler.postDelayed(this,16);return;}
  String copy=NativeRuntime.clipboard();if(!copy.isEmpty())((ClipboardManager)getSystemService(CLIPBOARD_SERVICE)).setPrimaryClip(ClipData.newPlainText("iotools明确复制",new String(android.util.Base64.decode(copy,android.util.Base64.DEFAULT),StandardCharsets.UTF_8)));
  if(!outputPending){
   byte[] bytes=NativeRuntime.read();
   if(outputObserver!=null&&bytes.length>0)outputObserver.accept(bytes);
   if(bytes.length>0){outputPending=true;int sequence=++outputSequence;web.evaluateJavascript("receiveTerminal("+JSONObject.quote(android.util.Base64.encodeToString(bytes,android.util.Base64.NO_WRAP))+","+sequence+")",null);}
  }
  int state=NativeRuntime.state();
  if(state==1)seenRunning=true;else if(seenRunning){seenRunning=false;if(!stopping)wanted=false;stopping=false;status.setText("终端已停止 · 点启动重新打开");}
  if(ready&&wanted&&state==0&&!starting)startTerminal();
  handler.postDelayed(this,16);
 }};
 @Override public void onCreate(Bundle state){
  super.onCreate(state);config=new File(getFilesDir(),"iotools.yaml");
  LinearLayout root=new LinearLayout(this);root.setOrientation(LinearLayout.VERTICAL);root.setBackgroundColor(Color.rgb(10,17,26));
  root.setOnApplyWindowInsetsListener((v,insets)->{v.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());return insets;});
  status=new TextView(this);status.setTextColor(Color.LTGRAY);status.setTextSize(12);status.setText("iotools · 本机终端 · 启动不联网");root.addView(status);
  LinearLayout first=row(root);
  button(first,"启动",()->{wanted=true;if(NativeRuntime.state()==1){NativeRuntime.resume();status.setText("原页面已恢复 · 不自动执行请求");}else startTerminal();});
  button(first,"停止",()->{NativeRuntime.pause();status.setText("活动任务已取消 · 编辑草稿仍保留");});
  button(first,"执行",()->send("\r"));button(first,"Tab",()->send("\t"));button(first,"Esc",()->send("\u001b"));
  button(first,"↑",()->send("\u001b[A"));button(first,"↓",()->send("\u001b[B"));button(first,"←",()->send("\u001b[D"));button(first,"→",()->send("\u001b[C"));
  LinearLayout second=row(root);
  button(second,"帮助",()->send("\u001bOP"));button(second,"视图",()->send("\u001bOQ"));button(second,"表单",()->send("\u001bOR"));
  button(second,"文件",()->send("\u001bOS"));button(second,"保存",()->send("\u0013"));button(second,"取消",()->send("\u001b[19~"));
  button(second,"键盘",()->{web.evaluateJavascript("terminalFocus()",null);((InputMethodManager)getSystemService(INPUT_METHOD_SERVICE)).showSoftInput(web,InputMethodManager.SHOW_IMPLICIT);});
  button(second,"输入",this::textInput);button(second,"更多",this::more);
  web=new WebView(this);web.setBackgroundColor(Color.BLACK);root.addView(web,new LinearLayout.LayoutParams(-1,0,1));setContentView(root);
  WebSettings settings=web.getSettings();settings.setJavaScriptEnabled(true);settings.setDomStorageEnabled(false);settings.setAllowFileAccess(false);settings.setAllowContentAccess(false);
  settings.setBlockNetworkLoads(true);settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);settings.setBuiltInZoomControls(false);
  WebViewAssetLoader assets=new WebViewAssetLoader.Builder().addPathHandler("/assets/",new WebViewAssetLoader.AssetsPathHandler(this)).build();
  web.setWebViewClient(new WebViewClient(){
   @Override public WebResourceResponse shouldInterceptRequest(WebView view,WebResourceRequest req){
    WebResourceResponse response=assets.shouldInterceptRequest(req.getUrl());
    return response!=null?response:new WebResourceResponse("text/plain","UTF-8",new ByteArrayInputStream(new byte[0]));
   }
   @Override public boolean shouldOverrideUrlLoading(WebView view,WebResourceRequest req){return true;}
   @Override public void onReceivedError(WebView view,WebResourceRequest request,WebResourceError error){if(request.isForMainFrame())runOnUiThread(()->status.setText("离线终端资源错误："+error.getErrorCode()));}
  });
  web.addJavascriptInterface(new Object(){
   @JavascriptInterface public void ready(int c,int r){runOnUiThread(()->{ready=true;resize(c,r);if(NativeRuntime.state()==1)NativeRuntime.resume();else startTerminal();});}
   @JavascriptInterface public void input(String text){runOnUiThread(()->send(text));}
   @JavascriptInterface public void resize(int c,int r){runOnUiThread(()->MainActivity.this.resize(c,r));}
   @JavascriptInterface public void outputDone(int sequence){runOnUiThread(()->{if(sequence==outputSequence)outputPending=false;});}
  },"IOTools");
  web.loadUrl("https://appassets.androidplatform.net/assets/index.html");
 }
 private LinearLayout row(LinearLayout root){HorizontalScrollView scroll=new HorizontalScrollView(this);LinearLayout row=new LinearLayout(this);row.setOrientation(LinearLayout.HORIZONTAL);scroll.addView(row);root.addView(scroll,new LinearLayout.LayoutParams(-1,-2));return row;}
 private void button(LinearLayout row,String label,Runnable action){Button b=new Button(this);b.setText(label);b.setTextSize(12);b.setMinWidth(0);b.setMinimumWidth(0);b.setPadding(12,0,12,0);b.setOnClickListener(v->action.run());row.addView(b,new LinearLayout.LayoutParams(-2,dp(42)));}
 private int dp(int n){return (int)(getResources().getDisplayMetrics().density*n);}
 private void resize(int c,int r){cols=Math.max(20,Math.min(300,c));rows=Math.max(8,Math.min(150,r));NativeRuntime.resize(cols,rows);}
 private void startTerminal(){
  if(!active||!ready||!wanted||starting||NativeRuntime.state()!=0)return;
  starting=true;
  android.content.SharedPreferences preferences=getPreferences(MODE_PRIVATE);
  int flags=(preferences.getBoolean("readonly",false)?1:0)|(preferences.getBoolean("history",false)?2:0);
  int result=NativeRuntime.start(config.getAbsolutePath(),cols,rows,flags);
  starting=false;
  if(result<0){wanted=false;status.setText("启动失败："+NativeRuntime.error());return;}
  stopping=false;
  status.setText("已启动 · "+cols+"×"+rows+" · 后台取消任务但保留编辑，不自动重放");
 }
 private void send(String text){if(!ready||NativeRuntime.state()==0)return;if(text.getBytes(StandardCharsets.UTF_8).length>65536){status.setText("输入超过64KiB");return;}if(NativeRuntime.input(text.getBytes(StandardCharsets.UTF_8))<0)status.setText(NativeRuntime.error());}
 private void textInput(){EditText text=new EditText(this);text.setMinLines(3);text.setHint("中文/多行文本；发送到当前输入位置");new AlertDialog.Builder(this).setTitle("输入文本").setView(text).setNegativeButton("取消",null).setPositiveButton("输入",(d,w)->web.evaluateJavascript("terminalPaste("+JSONObject.quote(text.getText().toString())+")",null)).show();}
 private void more(){
  String[] labels={"环境 F6","HTTP控制台 F7","OPC历史 F9","独立订阅 F10","HTTP历史 F11","配置轮换 F12","OPC四窗 Ctrl+U","上页","下页","反向Tab","导入配置","导出配置","使用说明","只读模式设置","历史保存设置","导入证书/数据附件","导出本机文件"};
  String[] keys={"\u001b[17~","\u001b[18~","\u001b[20~","\u001b[21~","\u001b[23~","\u001b[24~","\u0015","\u001b[5~","\u001b[6~","\u001b[Z"};
  new AlertDialog.Builder(this).setTitle("更多操作").setItems(labels,(d,w)->{
   if(w<keys.length){send(keys[w]);return;}
   if(w==10){Intent i=new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*").addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(i,IMPORT);}
   if(w==11){Intent i=new Intent(Intent.ACTION_CREATE_DOCUMENT).setType("application/yaml").putExtra(Intent.EXTRA_TITLE,"iotools.yaml").addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(i,EXPORT);}
   if(w==13){setting("readonly","只读模式","启用后禁止协议修改和历史删除；切换会取消活动任务，保留编辑草稿。");return;}
   if(w==14){setting("history","历史保存","启用后HTTP响应会保存到本机history.sqlite，可能含敏感信息；默认关闭。切换会取消活动任务，保留编辑草稿。");return;}
   if(w==15){Intent i=new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*").addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(i,IMPORT_FILE);return;}
   if(w==16){exportFileMenu();return;}
   if(w==12)new AlertDialog.Builder(this).setTitle("独立Android版").setMessage("真实Go TUI与协议引擎已内置，无需Termux。配置保存在应用私有目录；F3表单、F4文件、保存按钮=Ctrl+S。目标127.0.0.1是手机本机。仅明确执行才联网。HTTP明文由原生引擎按配置支持，WebView完全不联网。USB串口/后台常驻暂不提供；切后台取消活动请求，编辑草稿保留在内存；返回原页面，不自动执行。系统终止应用进程仍会丢失未保存草稿。当前为测试签名APK；更新签名不同时请先导出配置。").setPositiveButton("知道了",null).show();
  }).show();
 }
 @Override protected void onActivityResult(int request,int result,Intent data){
  super.onActivityResult(request,result,data);if(result!=RESULT_OK||data==null||data.getData()==null)return;Uri uri=data.getData();
  new Thread(()->{
   try{
    if(request==EXPORT||request==EXPORT_FILE){
     File selected=request==EXPORT?config:exportSource;if(selected==null)throw new IOException("未选择本机文件");
     try(InputStream in=new FileInputStream(selected);OutputStream out=getContentResolver().openOutputStream(uri,"w")){if(out==null)throw new IOException("无法打开目标");copy(in,out,64<<20);}
     runOnUiThread(()->status.setText("文件已导出到所选位置"));return;
    }
    if(request==IMPORT_FILE){
     String name="data.bin";
     try(android.database.Cursor c=getContentResolver().query(uri,new String[]{android.provider.OpenableColumns.DISPLAY_NAME},null,null,null)){if(c!=null&&c.moveToFirst())name=c.getString(0);}
     if(name==null)name="data.bin";name=name.replace('/','_').replace('\\','_').replaceAll("[\\p{Cntrl}]","_");if(name.length()>80)name=name.substring(name.length()-80);
     File attachment=File.createTempFile("attachment-","-"+name,getFilesDir());boolean complete=false;
     try(InputStream in=getContentResolver().openInputStream(uri);OutputStream out=new FileOutputStream(attachment)){if(in==null)throw new IOException("无法读取附件");copy(in,out,32<<20);complete=true;}finally{if(!complete)attachment.delete();}
     runOnUiThread(()->new AlertDialog.Builder(this).setTitle("附件已复制到应用私有目录").setMessage(attachment.getAbsolutePath()+"\n在F3/F4中引用此路径。").setNegativeButton("关闭",null).setPositiveButton("复制路径",(dialog,which)->((ClipboardManager)getSystemService(CLIPBOARD_SERVICE)).setPrimaryClip(ClipData.newPlainText("附件路径",attachment.getAbsolutePath()))).show());
     return;
    }
    if(request==IMPORT){
     File staged=File.createTempFile("import-",".yaml",getFilesDir());
     try(InputStream in=getContentResolver().openInputStream(uri);OutputStream out=new FileOutputStream(staged)){if(in==null)throw new IOException("无法读取所选文件");copy(in,out,1<<20);}
     if(NativeRuntime.validate(staged.getAbsolutePath())<0){staged.delete();throw new IOException(NativeRuntime.error());}
     runOnUiThread(()->new AlertDialog.Builder(this).setTitle("确认替换配置").setMessage("新文件已经校验。替换会丢弃当前未保存编辑；原磁盘配置会备份。取消可继续原草稿。")
      .setNegativeButton("取消",(dialog,which)->staged.delete()).setOnCancelListener(dialog->staged.delete()).setPositiveButton("替换并保留原文件备份",(dialog,which)->{
       wanted=false;stopping=true;NativeRuntime.stop();
       new Thread(()->{try{
        for(int n=0;n<100&&NativeRuntime.state()!=0;n++)Thread.sleep(50);
        if(NativeRuntime.state()!=0)throw new IOException("终端尚未停止，请重试");
        if(NativeRuntime.importConfig(staged.getAbsolutePath(),config.getAbsolutePath())<0)throw new IOException(NativeRuntime.error());
        runOnUiThread(()->status.setText("配置已导入并备份；点启动打开"));
       }catch(Exception error){runOnUiThread(()->status.setText("导入未完成："+error.getMessage()));}},"iotools-import").start();
      }).show());
    }
   }catch(Exception e){runOnUiThread(()->{wanted=false;status.setText("文件操作失败："+e.getMessage());});}
  },"iotools-files").start();
 }
 private void setting(String key,String title,String explanation){
  boolean current=getPreferences(MODE_PRIVATE).getBoolean(key,false);
  new AlertDialog.Builder(this).setTitle(title+"（当前"+(current?"启用":"关闭")+"）").setMessage(explanation).setNegativeButton("取消",null).setPositiveButton(current?"关闭":"明确启用",(d,w)->{
   getPreferences(MODE_PRIVATE).edit().putBoolean(key,!current).apply();
   int flags=(getPreferences(MODE_PRIVATE).getBoolean("readonly",false)?1:0)|(getPreferences(MODE_PRIVATE).getBoolean("history",false)?2:0);
   NativeRuntime.options(flags,config.getAbsolutePath());status.setText("设置已生效 · 编辑草稿保留");
  }).show();
 }
 private void exportFileMenu(){
  File[] list=getFilesDir().listFiles(f->f.isFile());if(list==null||list.length==0)return;
  java.util.Arrays.sort(list,(a,b)->a.getName().compareTo(b.getName()));
  String[] names=new String[list.length];for(int i=0;i<list.length;i++)names[i]=list[i].getName();
  new AlertDialog.Builder(this).setTitle("选择本机文件导出（可能含敏感数据）").setItems(names,(d,w)->{
   exportSource=list[w];Intent i=new Intent(Intent.ACTION_CREATE_DOCUMENT).setType("application/octet-stream").putExtra(Intent.EXTRA_TITLE,exportSource.getName()).addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(i,EXPORT_FILE);
  }).setNegativeButton("取消",null).show();
 }
 private static void copy(InputStream in,OutputStream out,int max)throws IOException{byte[] buf=new byte[8192];int total=0,n;while((n=in.read(buf))!=-1){total+=n;if(total>max)throw new IOException("配置超过1MiB");out.write(buf,0,n);}}
 @Override protected void onStart(){super.onStart();active=true;if(ready&&NativeRuntime.state()==1)NativeRuntime.resume();handler.post(poll);}
 @Override protected void onStop(){active=false;handler.removeCallbacks(poll);NativeRuntime.pause();super.onStop();}
 @Override protected void onDestroy(){web.removeJavascriptInterface("IOTools");web.destroy();super.onDestroy();}
 @Override public void onBackPressed(){send("\u001b");}
 public WebView terminalView(){return web;}
}
