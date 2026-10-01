package io.github.liveyum.iotools;
import static org.junit.Assert.*;
import static androidx.test.espresso.Espresso.onView;
import static androidx.test.espresso.matcher.ViewMatchers.withText;
import static androidx.test.espresso.action.ViewActions.click;

import android.content.Context;
import android.graphics.Bitmap;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.lifecycle.Lifecycle;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import org.junit.Test;
import org.junit.runner.RunWith;

@RunWith(AndroidJUnit4.class)
public class StandaloneTest {
 private String terminal(ActivityScenario<MainActivity> scenario){AtomicReference<String> result=new AtomicReference<>("");scenario.onActivity(a->result.set(a.terminalView().renderedText()));return result.get();}
 private long generation(ActivityScenario<MainActivity> scenario){AtomicLong value=new AtomicLong();scenario.onActivity(a->value.set(a.terminalView().renderedGeneration()));return value.get();}
 private void awaitGeneration(ActivityScenario<MainActivity> scenario,long previous)throws Exception{long end=System.currentTimeMillis()+30000;while(System.currentTimeMillis()<end){if(generation(scenario)>previous)return;Thread.sleep(100);}fail("native screen did not draw a new frame");}
 private void assertVisibleGlyphPixels(ActivityScenario<MainActivity> scenario){
  int[] bounds=new int[4];scenario.onActivity(a->{int[] location=new int[2];a.terminalView().getLocationOnScreen(location);bounds[0]=location[0];bounds[1]=location[1];bounds[2]=a.terminalView().getWidth();bounds[3]=a.terminalView().getHeight();});
  Bitmap screenshot=InstrumentationRegistry.getInstrumentation().getUiAutomation().takeScreenshot();assertNotNull("native screenshot",screenshot);int bright=0;
  for(int y=Math.max(0,bounds[1]);y<Math.min(screenshot.getHeight(),bounds[1]+bounds[3]);y+=2)for(int x=Math.max(0,bounds[0]);x<Math.min(screenshot.getWidth(),bounds[0]+bounds[2]);x+=2){int color=screenshot.getPixel(x,y);if(android.graphics.Color.red(color)>160&&android.graphics.Color.green(color)>160&&android.graphics.Color.blue(color)>160)bright++;}
  screenshot.recycle();assertTrue("actual canvas must contain visible foreground glyph pixels",bright>30);
 }
 private void awaitText(ActivityScenario<MainActivity> scenario,String text)throws Exception{
  long end=System.currentTimeMillis()+60000;String actual="";
  while(System.currentTimeMillis()<end){actual=terminal(scenario);if(actual.contains(text))return;Thread.sleep(100);}
  screenshot("failed-screen");fail("Missing "+text+" in "+actual+" native="+NativeRuntime.error());
 }
 private String shell(String command)throws Exception{
  android.os.ParcelFileDescriptor pipe=InstrumentationRegistry.getInstrumentation().getUiAutomation().executeShellCommand(command);
  try(InputStream input=new android.os.ParcelFileDescriptor.AutoCloseInputStream(pipe);ByteArrayOutputStream output=new ByteArrayOutputStream()){
   byte[] buffer=new byte[1024];int n;while((n=input.read(buffer))!=-1)output.write(buffer,0,n);return output.toString("UTF-8");
  }
 }
 private void screenshot(String name)throws Exception{
  // executeShellCommand does not interpret &&; issue each command separately.
  shell("mkdir -p /data/local/tmp/iotools-screenshots");
  shell("screencap -p /data/local/tmp/iotools-screenshots/"+name+".png");
  System.out.println("Screenshot: "+shell("ls -l /data/local/tmp/iotools-screenshots/"+name+".png"));
 }
 @Test public void standaloneRealHTTPChineseEditingPersistenceAndBackgroundStop()throws Exception{
  Context context=InstrumentationRegistry.getInstrumentation().getTargetContext();
  System.out.println("Renderer: native Android Canvas; API="+android.os.Build.VERSION.SDK_INT);
  NativeRuntime.stop();long stop=System.currentTimeMillis()+5000;while(NativeRuntime.state()!=0&&System.currentTimeMillis()<stop)Thread.sleep(50);
  AtomicInteger requests=new AtomicInteger();
  ServerSocket server=new ServerSocket(0,10,InetAddress.getByName("127.0.0.1"));
  Thread serving=new Thread(()->{try{while(!server.isClosed()){try(Socket s=server.accept()){
   BufferedReader reader=new BufferedReader(new InputStreamReader(s.getInputStream(),StandardCharsets.UTF_8));
   String line=reader.readLine();if(line==null)continue;while((line=reader.readLine())!=null&&!line.isEmpty()){}
   requests.incrementAndGet();byte[] body="{\"客服\":\"Android真实协议成功\"}".getBytes(StandardCharsets.UTF_8);
   s.getOutputStream().write(("HTTP/1.1 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: "+body.length+"\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));s.getOutputStream().write(body);
  }}}catch(IOException expected){}},"loopback-http");serving.start();
  File config=new File(context.getFilesDir(),"iotools.yaml");
  String yaml="version: 1\nprofiles:\n  local: {}\nrequests:\n  - id: customer\n    name: 客服验收\n    protocol: http\n    action: GET\n    endpoint: http://127.0.0.1:"+server.getLocalPort()+"/customer\n    timeout: 5s\n";
  try(OutputStream out=new FileOutputStream(config)){out.write(yaml.getBytes(StandardCharsets.UTF_8));}
  try(ActivityScenario<MainActivity> scenario=ActivityScenario.launch(MainActivity.class)){
   awaitText(scenario,"客服验收");assertEquals("startup must not connect",0,requests.get());assertVisibleGlyphPixels(scenario);screenshot("01-home");
   onView(withText("执行")).perform(click());awaitText(scenario,"Android真实协议成功");assertEquals(1,requests.get());screenshot("02-real-http");
   onView(withText("文件")).perform(click());awaitText(scenario,"请求配置 YAML");screenshot("03-config-editor");
   onView(withText("输入")).perform(click());
   onView(androidx.test.espresso.matcher.ViewMatchers.withHint("中文/多行文本；发送到当前输入位置")).perform(androidx.test.espresso.action.ViewActions.replaceText("# 中文备注 😀\n"));
   screenshot("03a-native-input-dialog");
   onView(withText("输入")).inRoot(androidx.test.espresso.matcher.RootMatchers.isDialog()).perform(click());
   awaitText(scenario,"中文备注");
   scenario.onActivity(a->{android.view.inputmethod.InputConnection input=a.terminalView().onCreateInputConnection(new android.view.inputmethod.EditorInfo());input.setComposingText("# 输入",1);input.setComposingText("# 输入法验收",1);input.commitText("# 输入法验收",1);input.finishComposingText();input.performEditorAction(android.view.inputmethod.EditorInfo.IME_ACTION_DONE);});
   awaitText(scenario,"输入法验收");
   // Switching apps before saving must preserve the exact editor draft.
   Thread.sleep(300);
   long beforePause=generation(scenario);
   scenario.moveToState(Lifecycle.State.CREATED);
   Thread.sleep(300);
   assertEquals("background retains TUI memory",1,NativeRuntime.state());
   scenario.moveToState(Lifecycle.State.RESUMED);
   awaitGeneration(scenario,beforePause);
   awaitText(scenario,"中文备注");
   assertEquals("background must not replay HTTP",1,requests.get());
   screenshot("03b-editor-resumed");
   onView(withText("保存")).perform(click());awaitText(scenario,"配置已保存");
   String saved=new String(java.nio.file.Files.readAllBytes(config.toPath()),StandardCharsets.UTF_8);
   assertTrue("Chinese/emoji bytes saved",saved.contains("中文备注 😀"));
   assertEquals("IME composition committed exactly once",1,saved.split("输入法验收",-1).length-1);
   assertTrue("IME Enter inserted one newline",saved.contains("# 输入法验收\nversion:"));
   onView(withText("Esc")).perform(click());
   scenario.moveToState(Lifecycle.State.CREATED);
   Thread.sleep(400);
   assertEquals("background retains idle native UI",1,NativeRuntime.state());
   scenario.moveToState(Lifecycle.State.RESUMED);Thread.sleep(500);assertEquals("no automatic replay",1,requests.get());
   onView(withText("启动")).perform(click());awaitText(scenario,"客服验收");assertEquals(1,requests.get());screenshot("04-reopened");
   scenario.onActivity(a->a.setRequestedOrientation(android.content.pm.ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE));
   Thread.sleep(1000);awaitText(scenario,"客服验收");screenshot("05-landscape");
  }finally{NativeRuntime.stop();server.close();serving.join(2000);}
 }
}
