"""Pin and verify official OFL fonts for entirely same-origin Web rendering."""
from pathlib import Path
import hashlib,json,urllib.request
root=Path(__file__).resolve().parents[2]
out=root/'mobile/build/web/fonts';out.mkdir(parents=True,exist_ok=True)
base='https://raw.githubusercontent.com/google/fonts/a85815a42757630ce188fdad368c2dfc444d4773/ofl/'
files=[
('NotoSansSC.ttf','notosanssc/NotoSansSC%5Bwght%5D.ttf','a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da'),
('NotoSansSC-OFL.txt','notosanssc/OFL.txt','1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9'),
('NotoEmoji.ttf','notoemoji/NotoEmoji%5Bwght%5D.ttf','de6c18832938afc99caf132b39d6a30a19bac7f2e812e28db2535b4608d27551'),
('NotoEmoji-OFL.txt','notoemoji/OFL.txt','500bb1ccf43df7bbb522112f9133a52b16e1c35e809632f5d8609b179152de5b'),
]
records=[]
for name,path,expected in files:
    target=out/name
    if not target.exists() or hashlib.sha256(target.read_bytes()).hexdigest()!=expected:
        with urllib.request.urlopen(base+path,timeout=120) as response: data=response.read(24*1024*1024)
        assert hashlib.sha256(data).hexdigest()==expected, f'Unexpected official font bytes: {name}'
        target.write_bytes(data)
    records.append({'file':name,'sha256':expected,'source':base+path})
(out/'manifest.json').write_text(json.dumps(records,indent=2)+'\n')
print('Verified same-origin Noto fonts and complete OFL notices')
