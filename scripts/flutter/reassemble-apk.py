#!/usr/bin/env python3
"""Reassemble authorized artifact chunks without changing the signed APK."""
import hashlib,json,pathlib,re,sys
root=pathlib.Path(sys.argv[1]).resolve();meta=json.loads((root/'aot-transport.json').read_text());name=meta['apk']
assert pathlib.Path(name).name==name and name.endswith('.apk') and '..' not in name
parts=meta['parts'];assert 1<=len(parts)<=3 and 0<meta['bytes']<=50*1024*1024
out=root/name;assert not out.exists(),'Refusing to overwrite an existing APK'
written=0;digest=hashlib.sha256()
try:
 with out.open('xb') as target:
  for index,part in enumerate(parts):
   assert part['name']==f'aot-transport.part{index:02d}' and 0<part['bytes']<=24*1024*1024
   p=root/part['name'];assert p.is_file() and not p.is_symlink();data=p.read_bytes()
   assert len(data)==part['bytes'] and hashlib.sha256(data).hexdigest()==part['sha256'],'Chunk hash mismatch'
   target.write(data);digest.update(data);written+=len(data)
 assert written==meta['bytes'] and digest.hexdigest()==meta['sha256'],'Reconstructed APK hash mismatch'
except BaseException:
 out.unlink(missing_ok=True);raise
print(f'{out}\nSHA256 {digest.hexdigest()}\n{written} bytes')
