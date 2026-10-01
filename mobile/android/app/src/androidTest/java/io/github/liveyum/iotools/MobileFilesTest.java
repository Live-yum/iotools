package io.github.liveyum.iotools;

import static org.junit.Assert.*;
import android.content.ContentResolver;
import android.database.Cursor;
import android.net.Uri;
import android.provider.DocumentsContract;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import org.junit.Test;
import org.junit.Before;
import org.junit.AfterClass;
import android.content.BroadcastReceiver;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.os.Handler;
import android.os.Looper;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.runner.RunWith;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.zip.*;

@RunWith(AndroidJUnit4.class)
public final class MobileFilesTest {
    @Before public void grantOnlyDisposableFixtureUris()throws Exception{fixtureGrant(false);}
    @AfterClass public static void revokeDisposableFixtureUris()throws Exception{fixtureGrant(true);}
    private static void fixtureGrant(boolean revoke)throws Exception{
        Context context=InstrumentationRegistry.getInstrumentation().getContext();
        Intent intent=new Intent().setComponent(new ComponentName(context.getPackageName(),FixtureGrantReceiver.class.getName())).putExtra("revoke",revoke);
        CountDownLatch done=new CountDownLatch(1);AtomicInteger status=new AtomicInteger();
        context.sendOrderedBroadcast(intent,null,new BroadcastReceiver(){@Override public void onReceive(Context ignored,Intent result){status.set(getResultCode());done.countDown();}},new Handler(Looper.getMainLooper()),0,null,null);
        assertTrue("fixture grant receiver timed out",done.await(15,TimeUnit.SECONDS));assertEquals("test-only URI grant failed",1,status.get());
    }
    private File temporary()throws IOException{return Files.createTempDirectory(InstrumentationRegistry.getInstrumentation().getTargetContext().getCacheDir().toPath(),"file-regression-").toFile();}
    private ContentResolver resolver(){return InstrumentationRegistry.getInstrumentation().getContext().getContentResolver();}
    private Uri document(String id){return DocumentsContract.buildDocumentUri(FileFixtureProvider.AUTHORITY,id);}
    private Uri create(String name)throws IOException{Uri value=DocumentsContract.createDocument(resolver(),document("root"),"application/octet-stream",name);assertNotNull(value);return value;}
    private boolean exists(Uri uri){try(Cursor cursor=resolver().query(uri,new String[]{DocumentsContract.Document.COLUMN_DOCUMENT_ID},null,null,null)){return cursor!=null&&cursor.moveToFirst();}}
    private byte[] read(Uri uri)throws IOException{try(InputStream input=resolver().openInputStream(uri);ByteArrayOutputStream output=new ByteArrayOutputStream()){MobileFiles.copyBounded(input,output,4096,-1,new MobileFiles.Cancel());return output.toByteArray();}}
    private static final class CountOutput extends OutputStream{long count;public void write(int value){count++;}public void write(byte[] bytes,int offset,int n){count+=n;}}
    private static final class GeneratedInput extends InputStream{long remaining;int reads;GeneratedInput(long count){remaining=count;}public int read(){reads++;if(remaining--<=0)return-1;return 0;}public int read(byte[] bytes,int offset,int n){reads++;if(remaining==0)return-1;int count=(int)Math.min(remaining,n);remaining-=count;return count;}}

    @Test public void longCountersStreamPastTwoGiBWithoutAllocatingBody()throws Exception{long length=(1L<<31)+17;GeneratedInput input=new GeneratedInput(length);CountOutput output=new CountOutput();assertEquals(length,MobileFiles.copyBounded(input,output,MobileFiles.MAX_LIMIT,length,new MobileFiles.Cancel()));assertEquals(length,output.count);assertTrue(input.reads>1);}
    @Test public void knownOversizeFailsBeforeReadOrWrite()throws Exception{GeneratedInput input=new GeneratedInput(100);CountOutput output=new CountOutput();try{MobileFiles.copyBounded(input,output,64,100,new MobileFiles.Cancel());fail("oversize accepted");}catch(IOException expected){}assertEquals(0,input.reads);assertEquals(0,output.count);}
    @Test public void unknownOversizeNeverWritesPastLimitAndCancellationWritesNothing()throws Exception{CountOutput output=new CountOutput();try{MobileFiles.copyBounded(new GeneratedInput(101),output,100,-1,new MobileFiles.Cancel());fail("oversize accepted");}catch(IOException expected){}assertTrue(output.count<=100);MobileFiles.Cancel cancelled=new MobileFiles.Cancel();cancelled.cancel();output=new CountOutput();try{MobileFiles.copyBounded(new GeneratedInput(10),output,10,10,cancelled);fail("cancel ignored");}catch(InterruptedIOException expected){}assertEquals(0,output.count);}
    @Test public void binaryContentAndZeroLengthAreExact()throws Exception{byte[] bytes={0,-1,-128,10,13,65};ByteArrayOutputStream output=new ByteArrayOutputStream();MobileFiles.copyBounded(new ByteArrayInputStream(bytes),output,bytes.length,bytes.length,new MobileFiles.Cancel());assertArrayEquals(bytes,output.toByteArray());assertEquals(0,MobileFiles.copyBounded(new ByteArrayInputStream(new byte[0]),new CountOutput(),0,0,new MobileFiles.Cancel()));}

    @Test public void providerUnknownLengthImportsBinaryAndSanitizesName()throws Exception{File root=temporary();try{File known=MobileFiles.importDocument(resolver(),document("source-name"),root,1024,new MobileFiles.Cancel());assertEquals(root.getCanonicalFile(),known.getParentFile());assertEquals(64,known.length());assertFalse(known.getName().contains("/"));File unknown=MobileFiles.importDocument(resolver(),document("source-unknown"),root,1024,new MobileFiles.Cancel());assertArrayEquals(Files.readAllBytes(known.toPath()),Files.readAllBytes(unknown.toPath()));}finally{MobileFiles.deleteOwned(root);}}
    @Test public void providerOversizeAndCancelLeaveNoPrivatePartialFile()throws Exception{File root=temporary();try{for(String id:new String[]{"source-declared-large","source-oversize"}){try{MobileFiles.importDocument(resolver(),document(id),root,128,new MobileFiles.Cancel());fail("oversize accepted");}catch(IOException expected){assertTrue(expected.getMessage().contains("超过"));}assertEquals(0,Objects.requireNonNull(root.list()).length);}MobileFiles.Cancel cancel=new MobileFiles.Cancel();cancel.cancel();try{MobileFiles.importDocument(resolver(),document("source-unknown"),root,128,cancel);fail("cancel ignored");}catch(InterruptedIOException expected){}assertEquals(0,Objects.requireNonNull(root.list()).length);}finally{MobileFiles.deleteOwned(root);}}
    @Test public void providerExportPreservesBinaryAndCleansCancelledNewDocument()throws Exception{File root=temporary();Uri output=create("binary.bin"),cancelled=create("cancelled.bin");try{File source=new File(root,"bytes.bin");byte[] bytes={0,-1,-128,42,10};Files.write(source.toPath(),bytes);MobileFiles.exportDocument(resolver(),output,source,1024,new MobileFiles.Cancel());assertArrayEquals(bytes,read(output));MobileFiles.Cancel cancel=new MobileFiles.Cancel();cancel.cancel();try{MobileFiles.exportDocument(resolver(),cancelled,source,1024,cancel);fail("cancel ignored");}catch(InterruptedIOException expected){}assertFalse(exists(cancelled));}finally{MobileFiles.deleteDocument(resolver(),output);MobileFiles.deleteDocument(resolver(),cancelled);MobileFiles.deleteOwned(root);}}
    @Test public void providerCleanupFailureIsDisclosed()throws Exception{File root=temporary();Uri output=create("undeletable.bin");try{File source=new File(root,"bytes.bin");Files.write(source.toPath(),new byte[]{1,2,3});MobileFiles.Cancel cancel=new MobileFiles.Cancel();cancel.cancel();try{MobileFiles.exportDocument(resolver(),output,source,1024,cancel);fail("cancel ignored");}catch(IOException expected){assertTrue(expected.getMessage().contains("无法删除"));}assertTrue(exists(output));}finally{MobileFiles.deleteOwned(root);}}
    @Test public void textExportKeepsChineseEmojiAcrossWriterChunks()throws Exception{StringBuilder text=new StringBuilder();for(int i=0;i<8191;i++)text.append('x');text.append("😀中文");Uri output=create("unicode.txt");try{MobileFiles.exportTextDocument(resolver(),output,text.toString(),16384,new MobileFiles.Cancel());try(InputStream input=resolver().openInputStream(output);ByteArrayOutputStream bytes=new ByteArrayOutputStream()){MobileFiles.copyBounded(input,bytes,16384,-1,new MobileFiles.Cancel());assertEquals(text.toString(),new String(bytes.toByteArray(),StandardCharsets.UTF_8));}}finally{MobileFiles.deleteDocument(resolver(),output);}}

    private byte[] zip(String[] names,byte[][] data)throws IOException{ByteArrayOutputStream bytes=new ByteArrayOutputStream();try(ZipOutputStream zip=new ZipOutputStream(bytes)){for(int i=0;i<names.length;i++){zip.putNextEntry(new ZipEntry(names[i]));zip.write(data[i]);zip.closeEntry();}}return bytes.toByteArray();}
    @Test public void zipPreservesRelativePathsAndExistingFiles()throws Exception{File root=temporary();try{File sentinel=new File(root,"keep.txt");Files.write(sentinel.toPath(),new byte[]{9});byte[] bytes=zip(new String[]{"main.yaml","refs/device.yaml","binary/sample.bin"},new byte[][]{"$ref: 'refs/device.yaml#/x'".getBytes(StandardCharsets.UTF_8),"x: 中文".getBytes(StandardCharsets.UTF_8),new byte[]{0,-1,7}});MobileFiles.Bundle bundle=MobileFiles.extractBundle(new ByteArrayInputStream(bytes),root,4096,bytes.length,new MobileFiles.Cancel());assertEquals(3,bundle.files.size());assertTrue(new File(bundle.directory,"refs/device.yaml").isFile());assertArrayEquals(new byte[]{0,-1,7},Files.readAllBytes(new File(bundle.directory,"binary/sample.bin").toPath()));assertArrayEquals(new byte[]{9},Files.readAllBytes(sentinel.toPath()));}finally{MobileFiles.deleteOwned(root);}}
    @Test public void zipRejectsTraversalAbsoluteAndDuplicatePathsAtomically()throws Exception{File root=temporary();try{for(String name:new String[]{"../escape.yaml","/absolute.yaml","C:/drive.yaml","a/../../escape","a\\escape","a//b","./main.yaml"}){byte[] bytes=zip(new String[]{name},new byte[][]{{1}});try{MobileFiles.extractBundle(new ByteArrayInputStream(bytes),root,4096,bytes.length,new MobileFiles.Cancel());fail("unsafe path accepted: "+name);}catch(IOException expected){}assertEquals(0,Objects.requireNonNull(root.list()).length);}byte[] duplicate=zip(new String[]{"same1.txt","same2.txt"},new byte[][]{{1},{2}});byte[] from="same2.txt".getBytes(StandardCharsets.UTF_8),to="same1.txt".getBytes(StandardCharsets.UTF_8);for(int i=0;i<=duplicate.length-from.length;i++){boolean match=true;for(int n=0;n<from.length;n++)if(duplicate[i+n]!=from[n]){match=false;break;}if(match)System.arraycopy(to,0,duplicate,i,to.length);}try{MobileFiles.extractBundle(new ByteArrayInputStream(duplicate),root,4096,duplicate.length,new MobileFiles.Cancel());fail("duplicate accepted");}catch(IOException expected){assertTrue(expected.getMessage().contains("重复"));}assertEquals(0,Objects.requireNonNull(root.list()).length);}finally{MobileFiles.deleteOwned(root);}}
    @Test public void zipExpansionAndCancellationCleanOwnedDirectory()throws Exception{File root=temporary();try{byte[] expanded=new byte[16384],bytes=zip(new String[]{"big.bin"},new byte[][]{expanded});try{MobileFiles.extractBundle(new ByteArrayInputStream(bytes),root,4096,bytes.length,new MobileFiles.Cancel());fail("expansion bound ignored");}catch(IOException expected){}assertEquals(0,Objects.requireNonNull(root.list()).length);MobileFiles.Cancel cancel=new MobileFiles.Cancel();cancel.cancel();try{MobileFiles.extractBundle(new ByteArrayInputStream(bytes),root,32768,bytes.length,cancel);fail("cancel ignored");}catch(InterruptedIOException expected){}assertEquals(0,Objects.requireNonNull(root.list()).length);}finally{MobileFiles.deleteOwned(root);}}
    @Test public void zipEntryCountAndPrivateSymlinksAreBounded()throws Exception{File root=temporary();try{String[] names=new String[MobileFiles.MAX_ZIP_ENTRIES+1];byte[][] data=new byte[names.length][];for(int i=0;i<names.length;i++){names[i]="file-"+i;data[i]=new byte[0];}byte[] bytes=zip(names,data);try{MobileFiles.extractBundle(new ByteArrayInputStream(bytes),root,1<<20,bytes.length,new MobileFiles.Cancel());fail("entry cap ignored");}catch(IOException expected){}assertEquals(0,Objects.requireNonNull(root.list()).length);File actual=new File(root,"actual");Files.write(actual.toPath(),new byte[]{1});Path link=new File(root,"link").toPath();Files.createSymbolicLink(link,actual.toPath());try{MobileFiles.privateFile(root,link.toFile(),true);fail("symlink accepted");}catch(IOException expected){}assertTrue(actual.isFile());}finally{MobileFiles.deleteOwned(root);}}
    @Test public void unicodeFileNamesStayWithinFilesystemByteLimit(){StringBuilder name=new StringBuilder();for(int i=0;i<100;i++)name.append("中😀");String safe=MobileFiles.safeName(name+".bin");assertTrue(safe.getBytes(StandardCharsets.UTF_8).length<=120);assertFalse(Character.isLowSurrogate(safe.charAt(0)));}
}
