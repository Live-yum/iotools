package io.github.liveyum.iotools;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
/** Test APK only. The provider owner grants only generated fixture documents,
 * to the fixed test target package; no app receives MANAGE_DOCUMENTS itself. */
public final class FixtureGrantReceiver extends BroadcastReceiver {
 @Override public void onReceive(Context context,Intent intent){
  Uri fixture=Uri.parse("content://"+FileFixtureProvider.AUTHORITY+"/");
  int access=Intent.FLAG_GRANT_READ_URI_PERMISSION|Intent.FLAG_GRANT_WRITE_URI_PERMISSION;
  try {
   if(intent.getBooleanExtra("revoke",false))context.revokeUriPermission("io.github.liveyum.iotools",fixture,access);
   else context.grantUriPermission("io.github.liveyum.iotools",fixture,access|Intent.FLAG_GRANT_PREFIX_URI_PERMISSION);
   setResultCode(1);
  } catch(RuntimeException failure){setResultCode(0);setResultData("Disposable fixture URI grant failed");}
 }
}
