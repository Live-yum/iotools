"""Reject test-only classes/components and unexpected exported release surfaces."""
from apk_manifest import elements

def check_manifest(rows):
    applications=[row['attributes'] for row in rows if row['tag']=='application']
    assert len(applications)==1 and not applications[0].get('debuggable',False),'Release app is debuggable'
    assert applications[0].get('allowBackup') is False,'Private config must not be backed up automatically'
    for row in rows:
        tag=row['tag'];attributes=row['attributes'];name=attributes.get('name','')
        assert not name.startswith('androidx.test.'),'Test-only dependency remains in release manifest: '+name
        assert tag!='instrumentation','Production APK contains instrumentation'
        if tag in ('activity','activity-alias'):
            assert tag=='activity' and name=='io.github.liveyum.iotools.MainActivity','Unexpected release activity: '+name
        if tag in ('provider','service'):
            assert attributes.get('exported') is False,'Unreviewed exported component: '+name
        if tag=='receiver' and attributes.get('exported') is not False:
            assert name=='androidx.profileinstaller.ProfileInstallReceiver' and attributes.get('permission')=='android.permission.DUMP','Unprotected exported receiver: '+name

def check_dex(data, name):
    for prefix in (b'Landroidx/test/',b'Lorg/junit/',b'Ljunit/',b'Lorg/hamcrest/',b'Ldev/flutter/plugins/integration_test/'):
        assert prefix not in data, f'Test-only class/reference remains in release {name}: {prefix.decode()}'

def check_apk(apk):
    check_manifest(elements(apk.read('AndroidManifest.xml')))
    dex=[name for name in apk.namelist() if name.startswith('classes') and name.endswith('.dex')]
    assert dex,'Release APK has no classes.dex'
    for name in dex:check_dex(apk.read(name),name)
