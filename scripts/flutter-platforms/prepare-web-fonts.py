"""Pin and verify official OFL fonts for entirely same-origin Web rendering."""
from pathlib import Path
import hashlib,json,os,shutil,urllib.request
root=Path(__file__).resolve().parents[2]
out=root/'mobile/build/web/fonts';out.mkdir(parents=True,exist_ok=True)
from font_assets import prepare_fonts
records=prepare_fonts(out)
# CanvasKit 3.35.7 downloads Roboto before main() unless FontManifest declares
# it. Use the official pinned Flutter SDK's unchanged font and license bytes.
flutter=os.environ.get('FLUTTER_ROOT')
if not flutter:
    executable=shutil.which('flutter')
    assert executable, 'Set FLUTTER_ROOT or put the pinned Flutter on PATH'
    flutter=str(Path(executable).resolve().parents[1])
sdk_fonts=Path(flutter)/'bin/cache/artifacts/material_fonts'
roboto=sdk_fonts/'Roboto-Regular.ttf'
expected='79e851404657dac2106b3d22ad256d47824a9a5765458edb72c9102a45816d95'
assert hashlib.sha256(roboto.read_bytes()).hexdigest()==expected, 'Unexpected pinned Flutter Roboto font'
font_assets=out.parent/'assets/fonts';font_assets.mkdir(parents=True,exist_ok=True)
shutil.copyfile(roboto,font_assets/'Roboto-Regular.ttf')
roboto_license=sdk_fonts/'Roboto_LICENSE.txt'
assert hashlib.sha256(roboto_license.read_bytes()).hexdigest()=='cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30', 'Unexpected pinned Roboto license'
shutil.copyfile(roboto_license,out/'Roboto-LICENSE.txt')
manifest_path=out.parent/'assets/FontManifest.json'
manifest=json.loads(manifest_path.read_text())
manifest=[family for family in manifest if family.get('family')!='Roboto']
manifest.append({'family':'Roboto','fonts':[{'asset':'fonts/Roboto-Regular.ttf'}]})
manifest_path.write_text(json.dumps(manifest,separators=(',',':')))
records.append({'file':'../assets/fonts/Roboto-Regular.ttf','sha256':expected,'source':'Pinned official Flutter 3.35.7 material_fonts/Roboto-Regular.ttf'})
(out/'manifest.json').write_text(json.dumps(records,indent=2)+'\n')
print('Verified same-origin Noto fonts and complete OFL notices')
