package io.github.liveyum.iotools;

import static org.junit.Assert.*;
import org.json.*;
import org.junit.Test;
import org.junit.runner.RunWith;
import androidx.test.ext.junit.runners.AndroidJUnit4;

@RunWith(AndroidJUnit4.class)
public class ModernModelTest {
 @Test public void exactJsonPreservesIntegersNestedArraysAndUnicode() throws Exception {
  String json="{\"max\":18446744073709551615,\"min\":-9223372036854775808,\"values\":[9007199254740993,\"中文😀\",{\"n\":18446744073709551615}]}";
  JSONObject value=ExactJson.object(json);
  assertEquals("18446744073709551615",value.get("max").toString());
  assertEquals("-9223372036854775808",value.get("min").toString());
  assertEquals("9007199254740993",value.getJSONArray("values").get(0).toString());
  assertEquals("中文😀",value.getJSONArray("values").getString(1));
  JSONObject roundtrip=ExactJson.object(value.toString());
  assertEquals("18446744073709551615",roundtrip.getJSONArray("values").getJSONObject(2).get("n").toString());
 }
 @Test public void exactJsonRejectsDuplicateTrailingAndMalformedNumbers() throws Exception {
  for(String value:new String[]{"{\"x\":1,\"x\":2}","[01]","[NaN]","[1,]","{}{}","{\"x\":\"bad\ntext\"}"}){
   try{ExactJson.parse(value);fail("invalid JSON accepted: "+value);}catch(JSONException expected){}
  }
 }
 @Test public void exactJsonNestingIsBounded() throws Exception {
  StringBuilder value=new StringBuilder();for(int i=0;i<70;i++)value.append('[');value.append('0');for(int i=0;i<70;i++)value.append(']');
  try{ExactJson.parse(value.toString());fail("deep JSON accepted");}catch(JSONException expected){assertTrue(expected.getMessage().contains("层级"));}
 }
 @Test public void usbJNIFailsClosedWithoutAnExplicitlyOpenedDevice() throws Exception {
  android.content.Context context=androidx.test.platform.app.InstrumentationRegistry.getInstrumentation().getTargetContext();
  java.io.File directory=new java.io.File(context.getFilesDir(),"usb-bridge-test");assertTrue(directory.isDirectory()||directory.mkdir());
  NativeRuntime.close();
  try {
   JSONObject opened=ExactJson.object(NativeRuntime.open(new java.io.File(directory,"iotools.yaml").getAbsolutePath(),"test",false,false));assertTrue(opened.toString(),opened.getBoolean("ok"));
   JSONObject request=NativeUi.json("id","usb-absent","protocol","modbus","action","read-holding","endpoint","usb://2147483647/0","timeout","1s","params",NativeUi.json("unit",1,"address",0,"count",1));
   JSONObject preview=ExactJson.object(NativeRuntime.command(NativeUi.json("op","preview","request",request).toString()));assertTrue(preview.toString(),preview.getBoolean("ok"));
   JSONObject run=ExactJson.object(NativeRuntime.command(NativeUi.json("op","run","token",preview.getJSONObject("data").getString("token"),"confirmed",false).toString()));assertTrue(run.toString(),run.getBoolean("ok"));
   long until=System.currentTimeMillis()+5000;String error=null;
   while(System.currentTimeMillis()<until&&error==null){JSONObject batch=ExactJson.object(NativeRuntime.command("{\"op\":\"events\"}")).getJSONObject("data");JSONArray events=batch.getJSONArray("events");for(int i=0;i<events.length();i++){JSONObject event=events.getJSONObject(i);if(event.getString("kind").equals("done")){JSONObject result=event.getJSONObject("data");assertEquals("failed",result.getString("status"));error=result.getString("error");}}Thread.sleep(20);}
   assertNotNull("USB request must stop without an opened device",error);assertTrue(error,error.contains("USB"));
  } finally {NativeRuntime.close();}
 }

}
