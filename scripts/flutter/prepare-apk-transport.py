#!/usr/bin/env python3
"""Keep exact APK bytes while fitting bounded artifact transfer clients."""
import hashlib,json,os,pathlib,sys
apk=pathlib.Path(sys.argv[1]);root=apk.parent
size=apk.stat().st_size
print(f'AOT APK size: {size} bytes')
assert size<=50*1024*1024,'AOT APK exceeds the delivery budget; retain all features and inspect packaging before delivery'
full=hashlib.sha256();parts=[]
with apk.open('rb') as source:
 for index,data in enumerate(iter(lambda:source.read(24*1024*1024),b'')):
  full.update(data)
  if size>30*1024*1024:
   name=f'aot-transport.part{index:02d}'
   (root/name).write_bytes(data)
   parts.append({'name':name,'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})
report={'apk':apk.name,'bytes':size,'sha256':full.hexdigest(),'parts':parts}
(root/'aot-transport.json').write_text(json.dumps(report,indent=2)+'\n')
if os.environ.get('GITHUB_OUTPUT'):
 with open(os.environ['GITHUB_OUTPUT'],'a') as target:target.write(f'count={len(parts)}\n')
print(f'Exact-byte transfer parts: {len(parts)}')
