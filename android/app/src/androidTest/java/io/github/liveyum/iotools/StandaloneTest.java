package io.github.liveyum.iotools;

import static org.junit.Assert.*;
import static androidx.test.espresso.Espresso.onView;
import static androidx.test.espresso.matcher.ViewMatchers.*;
import static androidx.test.espresso.action.ViewActions.*;
import android.content.Context;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import android.widget.EditText;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.lifecycle.Lifecycle;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.concurrent.atomic.*;
import org.junit.Test;
import org.junit.runner.RunWith;

@RunWith(AndroidJUnit4.class)
public class StandaloneTest {
 private static String visible(View view){StringBuilder out=new StringBuilder();if(view.getVisibility()!=View.VISIBLE)return "";if(view instanceof TextView)out.append(((TextView)view).getText()).append('\n');if(view instanceof ViewGroup){ViewGroup group=(ViewGroup)view;for(int i=0;i<group.getChildCount();i++)out.append(visible(group.getChildAt(i)));}return out.toString();}
 private String screen(ActivityScenario<MainActivity> scenario){AtomicReference<String> text=new AtomicReference<>("");scenario.onActivity(a->text.set(visible(a.getWindow().getDecorView())));return text.get();}
 private void awaitText(ActivityScenario<MainActivity> scenario,String expected)throws Exception {long end=System.currentTimeMillis()+30000;String actual="";while(System.currentTimeMillis()<end){actual=screen(scenario);if(actual.contains(expected))return;Thread.sleep(100);}screenshot("failed-screen");fail("Native views missing "+expected+" in "+actual);}
 private void awaitSaved(File file,String expected)throws Exception {long end=System.currentTimeMillis()+10000;while(System.currentTimeMillis()<end){if(new String(Files.readAllBytes(file.toPath()),StandardCharsets.UTF_8).contains(expected))return;Thread.sleep(100);}fail("Saved file missing expected text");}
 private String shell(String command)throws Exception {android.os.ParcelFileDescriptor pipe=InstrumentationRegistry.getInstrumentation().getUiAutomation().executeShellCommand(command);try(InputStream in=new android.os.ParcelFileDescriptor.AutoCloseInputStream(pipe);ByteArrayOutputStream out=new ByteArrayOutputStream()){byte[] b=new byte[1024];int n;while((n=in.read(b))!=-1)out.write(b,0,n);return out.toString("UTF-8");}}
 private void screenshot(String name)throws Exception {shell("mkdir -p /data/local/tmp/iotools-screenshots");shell("screencap -p /data/local/tmp/iotools-screenshots/"+name+".png");}
 @Test public void modernNativeHTTPWriteConfirmationEditingAndLifecycle()throws Exception {
  Context context=InstrumentationRegistry.getInstrumentation().getTargetContext();NativeRuntime.close();
  AtomicInteger reads=new AtomicInteger(),writes=new AtomicInteger();
  ServerSocket server=new ServerSocket(0,10,InetAddress.getByName("127.0.0.1"));
  Thread serving=new Thread(()->{try{while(!server.isClosed())try(Socket socket=server.accept()){
   BufferedReader reader=new BufferedReader(new InputStreamReader(socket.getInputStream(),StandardCharsets.UTF_8));String first=reader.readLine();if(first==null)continue;String line;while((line=reader.readLine())!=null&&!line.isEmpty()){}if(first.startsWith("POST"))writes.incrementAndGet();else reads.incrementAndGet();
   byte[] body="{\"客服\":\"原生移动界面请求成功\",\"状态\":200}".getBytes(StandardCharsets.UTF_8);socket.getOutputStream().write(("HTTP/1.1 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: "+body.length+"\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));socket.getOutputStream().write(body);
  }}catch(IOException expected){}},"modern-loopback-http");serving.start();
  File config=new File(context.getFilesDir(),"iotools.yaml");
  String base="version: 1\nprofiles:\n  local: {}\nrequests:\n  - id: customer\n    name: 客服验收\n    protocol: http\n    action: GET\n    endpoint: http://127.0.0.1:"+server.getLocalPort()+"/customer\n    timeout: 5s\n  - id: customer-write\n    name: 客服写入验收\n    protocol: http\n    action: POST\n    endpoint: http://127.0.0.1:"+server.getLocalPort()+"/customer\n    timeout: 5s\n";
  Files.write(config.toPath(),base.getBytes(StandardCharsets.UTF_8));
  try(ActivityScenario<MainActivity> scenario=ActivityScenario.launch(MainActivity.class)){
   awaitText(scenario,"客服验收");assertEquals("startup does not connect",0,reads.get()+writes.get());screenshot("01-modern-requests");
   onView(withText("客服验收")).perform(click());awaitText(scenario,"连接地址");
   onView(withId(R.id.request_endpoint)).check(androidx.test.espresso.assertion.ViewAssertions.matches(isAssignableFrom(EditText.class)));screenshot("02-modern-form");
   onView(withId(R.id.run_request)).perform(scrollTo(),click());awaitText(scenario,"原生移动界面请求成功");assertEquals(1,reads.get());screenshot("03-modern-http-result");
   onView(withId(R.id.nav_requests)).perform(click());awaitText(scenario,"客服写入验收");onView(withText("客服写入验收")).perform(scrollTo(),click());
   onView(withId(R.id.run_request)).perform(scrollTo(),click());onView(withText("确认执行写入操作")).check(androidx.test.espresso.assertion.ViewAssertions.matches(isDisplayed()));screenshot("04-write-review");onView(withText("取消")).inRoot(androidx.test.espresso.matcher.RootMatchers.isDialog()).perform(click());assertEquals("cancelled write never sent",0,writes.get());
   onView(withId(R.id.run_request)).perform(scrollTo(),click());onView(withText("确认执行")).inRoot(androidx.test.espresso.matcher.RootMatchers.isDialog()).perform(click());awaitText(scenario,"原生移动界面请求成功");assertEquals(1,writes.get());
   onView(withText("YAML")).perform(click());awaitText(scenario,"请求配置 YAML");
   String draft=base+"# 中文草稿 😀\n";onView(withId(R.id.yaml_editor)).perform(scrollTo(),replaceText(draft),closeSoftKeyboard());
   scenario.onActivity(a->{EditText editor=a.findViewById(R.id.yaml_editor);editor.setSelection(editor.length());android.view.inputmethod.InputConnection input=editor.onCreateInputConnection(new android.view.inputmethod.EditorInfo());input.setComposingText("# 输入",1);input.setComposingText("# 输入法验收",1);input.commitText("# 输入法验收\n",1);input.finishComposingText();});
   scenario.moveToState(Lifecycle.State.CREATED);Thread.sleep(200);scenario.moveToState(Lifecycle.State.RESUMED);awaitText(scenario,"中文草稿 😀");awaitText(scenario,"输入法验收");assertEquals("resume does not replay",2,reads.get()+writes.get());screenshot("05-native-yaml-draft");
   onView(withId(R.id.save_yaml)).perform(scrollTo(),click());
   onView(withText("保存并替换")).inRoot(androidx.test.espresso.matcher.RootMatchers.isDialog()).perform(click());
   awaitSaved(config,"中文草稿 😀");String saved=new String(Files.readAllBytes(config.toPath()),StandardCharsets.UTF_8);assertEquals("IME commit exactly once",1,saved.split("输入法验收",-1).length-1);
   scenario.onActivity(a->a.setRequestedOrientation(android.content.pm.ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE));Thread.sleep(600);awaitText(scenario,"请求配置 YAML");screenshot("06-modern-landscape");
   onView(withId(R.id.nav_settings)).perform(click());awaitText(scenario,"设置");screenshot("07-modern-settings");
   assertEquals("navigation never replays",2,reads.get()+writes.get());
  }finally{NativeRuntime.close();server.close();serving.join(2000);}
 }
}
