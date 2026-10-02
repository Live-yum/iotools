package io.github.liveyum.iotools;
import static org.junit.Assert.*;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import java.io.*;
import java.nio.file.*;
import java.util.*;
import org.junit.Test;
import org.junit.runner.RunWith;
@RunWith(AndroidJUnit4.class)
public final class PlatformSettingsTest {
 private File root()throws IOException{return Files.createTempDirectory(InstrumentationRegistry.getInstrumentation().getTargetContext().getCacheDir().toPath(),"settings-").toFile();}
 private void reject(File root,Map<String,Object> value)throws Exception{try{MainActivity.validateSettings(root,value);fail("unsafe setting accepted");}catch(IOException expected){}}
 @Test public void rejectsSecretsAndNonBooleanOptionsWithoutWriting()throws Exception{File root=root();try{for(String name:Arrays.asList("password","draft","token","yaml","secret"))reject(root,Collections.singletonMap(name,"private-value"));reject(root,Collections.singletonMap("readOnly","false"));reject(root,Collections.singletonMap("history",1));reject(root,Collections.singletonMap("theme","invalid"));assertEquals(0,Objects.requireNonNull(root.list()).length);}finally{MobileFiles.deleteOwned(root);}}
 @Test public void collectionCannotEscapeOrBecomeUri()throws Exception{File root=root();try{for(String path:Arrays.asList("../other","/absolute","https://example.com/secret","folder//x","./x","a\\b","a\nb"))reject(root,Collections.singletonMap("collection",path));Files.createDirectories(new File(root,"bundle").toPath());MainActivity.validateSettings(root,Collections.singletonMap("collection","bundle/中文.yaml"));Files.createSymbolicLink(new File(root,"link").toPath(),new File(root,"bundle").toPath());reject(root,Collections.singletonMap("collection","link/main.yaml"));}finally{MobileFiles.deleteOwned(root);}}
 @Test public void onlyExplicitSmallPreferencesAreAccepted()throws Exception{File root=root();try{Map<String,Object> values=new HashMap<>();values.put("theme","system");values.put("readOnly",true);values.put("history",false);values.put("collection","iotools.yaml");MainActivity.validateSettings(root,values);assertEquals(0,Objects.requireNonNull(root.list()).length);}finally{MobileFiles.deleteOwned(root);}}
}
