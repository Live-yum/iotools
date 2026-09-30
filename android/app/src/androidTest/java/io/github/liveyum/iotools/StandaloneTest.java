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
 private String terminal(ActivityScenario<MainActivity> scenario)throws Exception{
  CountDownLatch latch=new CountDownLatch(1);AtomicReference<String> result=new AtomicReference<>("");
  scenario.onActivity(a->a.terminalView().evaluateJavascript("terminalText()",s->{result.set(s);latch.countDown();}));
  assertTrue("terminal callback",latch.await(10,TimeUnit.SECONDS));return result.get();
 }
 private void awaitText(ActivityScenario<MainActivity> scenario,String text)throws Exception{
  long end=System.currentTimeMillis()+20000;String actual="";
  while(System.currentTimeMillis()<end){actual=terminal(scenario);if(actual.contains(text))return;Thread.sleep(100);}
  fail("Missing "+text+" in "+actual);
 }
 private void screenshot(String name)throws Exception{
  Context context=InstrumentationRegistry.getInstrumentation().getTargetContext();
  File directory=new File(context.getExternalFilesDir(null),"screenshots");directory.mkdirs();
  Bitmap image=InstrumentationRegistry.getInstrumentation().getUiAutomation().takeScreenshot();
  assertNotNull(image);try(OutputStream out=new FileOutputStream(new File(directory,name+".png"))){assertTrue(image.compress(Bitmap.CompressFormat.PNG,100,out));}
 }
 @Test public void standaloneRealHTTPChineseEditingPersistenceAndBackgroundStop()throws Exception{
  Context context=InstrumentationRegistry.getInstrumentation().getTargetContext();
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
   awaitText(scenario,"客服验收");assertEquals("startup must not connect",0,requests.get());screenshot("01-home");
   onView(withText("执行")).perform(click());awaitText(scenario,"Android真实协议成功");assertEquals(1,requests.get());screenshot("02-real-http");
   onView(withText("文件")).perform(click());awaitText(scenario,"请求配置 YAML");screenshot("03-config-editor");
   // Bracketed paste goes through the same terminal input path as IME/multiline input.
   scenario.onActivity(a->a.terminalView().evaluateJavascript("terminalPaste('# 中文备注 😀\\n')",null));
   onView(withText("保存")).perform(click());awaitText(scenario,"配置已保存");
   String saved=new String(java.nio.file.Files.readAllBytes(config.toPath()),StandardCharsets.UTF_8);
   assertTrue("Chinese/emoji bytes saved",saved.contains("中文备注 😀"));
   onView(withText("Esc")).perform(click());
   scenario.moveToState(Lifecycle.State.CREATED);
   long deadline=System.currentTimeMillis()+10000;while(NativeRuntime.state()!=0&&System.currentTimeMillis()<deadline)Thread.sleep(50);
   assertEquals("background stops native runtime",0,NativeRuntime.state());
   scenario.moveToState(Lifecycle.State.RESUMED);Thread.sleep(500);assertEquals("no automatic replay",1,requests.get());
   onView(withText("启动")).perform(click());awaitText(scenario,"客服验收");assertEquals(1,requests.get());screenshot("04-reopened");
   scenario.onActivity(a->a.setRequestedOrientation(android.content.pm.ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE));
   Thread.sleep(1000);awaitText(scenario,"客服验收");screenshot("05-landscape");
  }finally{NativeRuntime.stop();server.close();serving.join(2000);}
 }
}
