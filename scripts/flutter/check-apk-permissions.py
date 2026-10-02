#!/usr/bin/env python3
"""Validate the final merged APK permissions, including transitive manifests."""
import pathlib,re,sys
text=pathlib.Path(sys.argv[1]).read_text()
actual=set(re.findall(r"uses-permission: name='([^']+)'",text))
allowed={'android.permission.INTERNET','io.github.liveyum.iotools.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION'}
assert actual<=allowed, f'Unexpected permissions in delivered APK: {sorted(actual-allowed)}'
assert 'android.permission.INTERNET' in actual,'Protocol networking permission missing'
print('Merged production permissions match the bounded allow-list')
