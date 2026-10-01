"""Bundle offline CJK/emoji fonts for Linux engines without system fallbacks."""
from pathlib import Path
import json
from font_assets import prepare_fonts
root=Path(__file__).resolve().parents[2]
out=root/'mobile/native/fonts'
records=prepare_fonts(out)
(out/'manifest.json').write_text(json.dumps(records,indent=2)+'\n')
print('Native CJK/emoji fonts and OFL notices verified')
