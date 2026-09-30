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
 private boolean active,ready,wanted=true,starting,seenRunning;
 private int cols=80,rows=24;
 private static final int IMPORT=21,EXPORT=22;
 private File config;
 private final Runnable poll=new Runnable(){public void run(){
  if(!active)return;
  String copy=NativeRuntime.clipboard();if(!copy.isEmpty())((ClipboardManager)getSystemService(CLIPBOARD_SERVICE)).setPrimaryClip(ClipData.newPlainText("iotools明确复制",new String(android.util.Base64.decode(copy,android.util.Base64.DEFAULT),StandardCharsets.UTF_8)));
  byte[] bytes=NativeRuntime.read();
  if(bytes.length>0)web.evaluateJavascript("receiveTerminal("+JSONObject.quote(android.util.Base64.encodeToString(bytes,android.util.Base64.NO_WRAP))+")",null);
  int state=NativeRuntime.state();
  if(state==1)seenRunning=true;else if(seenRunning){seenRunning=false;wanted=false;status.setText("终端已停止 · 点启动重新打开");}
  if(ready&&wanted&&state==0&&!starting)startTerminal();
  handler.postDelayed(this,16);
 }};
 @Override public void onCreate(Bundle state){
  super.onCreate(state);config=new File(getFilesDir(),"iotools.yaml");
  LinearLayout root=new LinearLayout(this);root.setOrientation(LinearLayout.VERTICAL);root.setBackgroundColor(Color.rgb(10,17,26));
  root.setOnApplyWindowInsetsListener((v,insets)->{v.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());return insets;});
  status=new TextView(this);status.setTextColor(Color.LTGRAY);status.setTextSize(12);status.setText("iotools · 本机终端 · 启动不联网");root.addView(status);
  LinearLayout first=row(root);
  button(first,"启动",()->{wanted=true;startTerminal();});
  button(first,"停止",()->{wanted=false;NativeRuntime.stop();status.setText("已请求停止，后台不会重放请求");});
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
  });
  web.addJavascriptInterface(new Object(){
   @JavascriptInterface public void ready(int c,int r){runOnUiThread(()->{ready=true;resize(c,r);startTerminal();});}
   @JavascriptInterface public void input(String text){runOnUiThread(()->send(text));}
   @JavascriptInterface public void resize(int c,int r){runOnUiThread(()->MainActivity.this.resize(c,r));}
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
  int result=NativeRuntime.start(config.getAbsolutePath(),cols,rows);
  starting=false;
  if(result<0){wanted=false;status.setText("启动失败："+NativeRuntime.error());return;}
  status.setText("已启动 · "+cols+"×"+rows+" · 切到后台会停止，返回不自动执行请求");
 }
 private void send(String text){if(!ready||NativeRuntime.state()==0)return;if(text.getBytes(StandardCharsets.UTF_8).length>65536){status.setText("输入超过64KiB");return;}if(NativeRuntime.input(text.getBytes(StandardCharsets.UTF_8))<0)status.setText(NativeRuntime.error());}
 private void textInput(){EditText text=new EditText(this);text.setMinLines(3);text.setHint("中文/多行文本；发送到当前输入位置");new AlertDialog.Builder(this).setTitle("输入文本").setView(text).setNegativeButton("取消",null).setPositiveButton("输入",(d,w)->web.evaluateJavascript("terminalPaste("+JSONObject.quote(text.getText().toString())+")",null)).show();}
 private void more(){
  String[] labels={"环境 F6","HTTP控制台 F7","OPC历史 F9","独立订阅 F10","HTTP历史 F11","配置轮换 F12","OPC四窗 Ctrl+U","上页","下页","反向Tab","导入配置","导出配置","使用说明"};
  String[] keys={"\u001b[17~","\u001b[18~","\u001b[20~","\u001b[21~","\u001b[23~","\u001b[24~","\u0015","\u001b[5~","\u001b[6~","\u001b[Z"};
  new AlertDialog.Builder(this).setTitle("更多操作").setItems(labels,(d,w)->{
   if(w<keys.length){send(keys[w]);return;}
   if(w==10){wanted=false;NativeRuntime.stop();Intent i=new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*").addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(i,IMPORT);}
   if(w==11){Intent i=new Intent(Intent.ACTION_CREATE_DOCUMENT).setType("application/yaml").putExtra(Intent.EXTRA_TITLE,"iotools.yaml").addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(i,EXPORT);}
   if(w==12)new AlertDialog.Builder(this).setTitle("独立Android版").setMessage("真实Go TUI与协议引擎已内置，无需Termux。配置保存在应用私有目录；F3表单、F4文件、保存按钮=Ctrl+S。目标127.0.0.1是手机本机。仅明确执行才联网。HTTP明文由原生引擎按配置支持，WebView完全不联网。USB串口/后台常驻暂不提供；切后台停止活动请求，返回仅重开空闲界面。当前为测试签名APK；更新签名不同时请先导出配置。").setPositiveButton("知道了",null).show();
  }).show();
 }
 @Override protected void onActivityResult(int request,int result,Intent data){
  super.onActivityResult(request,result,data);if(result!=RESULT_OK||data==null||data.getData()==null)return;Uri uri=data.getData();
  new Thread(()->{
   try{
    if(request==EXPORT){
     try(InputStream in=new FileInputStream(config);OutputStream out=getContentResolver().openOutputStream(uri,"w")){if(out==null)throw new IOException("无法打开目标");copy(in,out,1<<20);}
     runOnUiThread(()->status.setText("配置已导出到所选文件"));return;
    }
    if(request==IMPORT){
     for(int n=0;n<100&&NativeRuntime.state()!=0;n++)Thread.sleep(50);
     if(NativeRuntime.state()!=0)throw new IOException("终端尚未停止，请稍后重试");
     File staged=File.createTempFile("import-",".yaml",getFilesDir());
     try(InputStream in=getContentResolver().openInputStream(uri);OutputStream out=new FileOutputStream(staged)){if(in==null)throw new IOException("无法读取所选文件");copy(in,out,1<<20);}
     int code=NativeRuntime.importConfig(staged.getAbsolutePath(),config.getAbsolutePath());
     if(code<0){staged.delete();throw new IOException(NativeRuntime.error());}
     runOnUiThread(()->{status.setText("配置已验证并导入，原配置已备份；点启动打开");wanted=false;});
    }
   }catch(Exception e){runOnUiThread(()->{wanted=false;status.setText("文件操作失败："+e.getMessage());});}
  },"iotools-files").start();
 }
 private static void copy(InputStream in,OutputStream out,int max)throws IOException{byte[] buf=new byte[8192];int total=0,n;while((n=in.read(buf))!=-1){total+=n;if(total>max)throw new IOException("配置超过1MiB");out.write(buf,0,n);}}
 @Override protected void onStart(){super.onStart();active=true;handler.post(poll);}
 @Override protected void onStop(){active=false;wanted=false;handler.removeCallbacks(poll);NativeRuntime.stop();super.onStop();}
 @Override protected void onDestroy(){web.removeJavascriptInterface("IOTools");web.destroy();super.onDestroy();}
 @Override public void onBackPressed(){send("\u001b");}
 public WebView terminalView(){return web;}
}
