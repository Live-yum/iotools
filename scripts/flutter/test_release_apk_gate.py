import unittest
from release_apk_gate import check_manifest,check_dex

def manifest():
    return [{'tag':'application','attributes':{'allowBackup':False}},
            {'tag':'activity','attributes':{'name':'io.github.liveyum.iotools.MainActivity','exported':True}},
            {'tag':'provider','attributes':{'name':'androidx.startup.InitializationProvider','exported':False}},
            {'tag':'receiver','attributes':{'name':'androidx.profileinstaller.ProfileInstallReceiver','exported':True,'permission':'android.permission.DUMP'}}]

class ReleaseGateTests(unittest.TestCase):
    def test_normal_launcher_and_protected_platform_receiver(self):
        check_manifest(manifest());check_dex(b'Lio/github/liveyum/iotools/TestProtocol;', 'classes.dex')
    def test_every_observed_test_activity_is_rejected_even_if_not_exported(self):
        for suffix in ['BootstrapActivity','EmptyActivity','EmptyFloatingActivity']:
            for exported in [False,True]:
                with self.subTest(suffix=suffix,exported=exported):
                    rows=manifest()+[{'tag':'activity','attributes':{'name':'androidx.test.core.app.InstrumentationActivityInvoker$'+suffix,'exported':exported}}]
                    with self.assertRaisesRegex(AssertionError,'Test-only dependency'):check_manifest(rows)
    def test_unprotected_other_exports_and_instrumentation_are_rejected(self):
        for row in [{'tag':'receiver','attributes':{'name':'other','exported':True}}, {'tag':'provider','attributes':{'name':'other','exported':True}}, {'tag':'service','attributes':{'name':'other','exported':True}}, {'tag':'instrumentation','attributes':{}}]:
            with self.subTest(row=row),self.assertRaises(AssertionError):check_manifest(manifest()+[row])
    def test_native_test_classes_rejected_without_manifest_entry(self):
        for prefix in [b'Landroidx/test/runner/AndroidJUnitRunner;',b'Lorg/junit/Test;',b'Ljunit/framework/TestCase;',b'Lorg/hamcrest/Matcher;',b'Ldev/flutter/plugins/integration_test/IntegrationTestPlugin;']:
            with self.subTest(prefix=prefix),self.assertRaisesRegex(AssertionError,'Test-only'):check_dex(b'dex\n035\0'+prefix,'classes2.dex')
    def test_debuggable_and_private_backup_rejected(self):
        for attrs in [{'debuggable':True,'allowBackup':False},{'allowBackup':True}]:
            rows=manifest();rows[0]['attributes']=attrs
            with self.assertRaises(AssertionError):check_manifest(rows)
if __name__=='__main__':unittest.main()
