from PIL import Image,ImageDraw,ImageFont
import json,pathlib,sys
root=pathlib.Path(sys.argv[1]) if len(sys.argv)>1 else pathlib.Path(__file__).parent
font=ImageFont.truetype('/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc',20,index=7)
for path in root.glob('*.json'):
 data=json.loads(path.read_text());w,h=data['Width'],data['Height'];im=Image.new('RGB',(w*10,h*22),'black');d=ImageDraw.Draw(im)
 skip=-1
 for i,c in enumerate(data['Cells']):
  if i<=skip:continue
  width=max(1,c.get('W',1));skip=i+width-1
  x,y=i%w*10,i//w*22;d.rectangle((x,y,x+width*10-1,y+21),fill=((c['BG']>>16)&255,(c['BG']>>8)&255,c['BG']&255))
 skip=-1
 for i,c in enumerate(data['Cells']):
  if i<=skip:continue
  skip=i+max(1,c.get('W',1))-1
  text=c['Text'];x,y=i%w*10,i//w*22
  if text and text!=' ':d.text((x,y-2),text,font=font,fill=((c['FG']>>16)&255,(c['FG']>>8)&255,c['FG']&255))
 im.save(path.with_suffix('.png'))
