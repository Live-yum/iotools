package io.github.liveyum.iotools;

import android.content.Context;
import android.graphics.*;
import android.text.*;
import android.view.*;
import android.view.inputmethod.*;
import java.nio.*;
import java.nio.charset.StandardCharsets;

/** Native renderer for actual tcell screen cells, with no ANSI/HTML interpreter. */
public final class TerminalCanvas extends View {
 public interface Host {void resize(int cols,int rows);void key(String name,int rune,int modifiers);void input(String text);void mouse(int x,int y,boolean down);}
 private final Host host;
 private final Paint paint=new Paint(Paint.ANTI_ALIAS_FLAG);
 private final Typeface regular=Typeface.create("monospace",Typeface.NORMAL),bold=Typeface.create("monospace",Typeface.BOLD),italic=Typeface.create("monospace",Typeface.ITALIC),boldItalic=Typeface.create("monospace",Typeface.BOLD_ITALIC);
 private final float cellWidth,cellHeight,baseline,textSize;
 private Frame frame;
 private long frameGeneration,drawnGeneration;
 private String drawnText="",composing="";
 private boolean blink=true,dragged;
 private float touchX,touchY,lastY;
 private final Runnable blinkTick=new Runnable(){public void run(){blink=!blink;invalidate();if(isShown())postDelayed(this,500);}};
 private static final class Style {int fg,bg,attr,underline,underlineColor;}
 private static final class Cell {int palette,width;String text;}
 private static final class Frame {int cols,rows,cursorX,cursorY;boolean cursorVisible;Style[] styles;Cell[] cells;}
 public TerminalCanvas(Context context,Host host){
  super(context);this.host=host;textSize=13*getResources().getDisplayMetrics().scaledDensity;
  paint.setTypeface(regular);paint.setTextSize(textSize);Paint.FontMetrics metrics=paint.getFontMetrics();
  cellWidth=(float)Math.ceil(paint.measureText("M"));cellHeight=(float)Math.ceil(metrics.descent-metrics.ascent+2);baseline=1-metrics.ascent;
  setFocusable(true);setFocusableInTouchMode(true);setBackgroundColor(Color.BLACK);setContentDescription("iotools 原生终端");
 }
 @Override protected void onMeasure(int widthSpec,int heightSpec){
  int minimum=(int)Math.ceil(80*cellWidth),width=MeasureSpec.getMode(widthSpec)==MeasureSpec.UNSPECIFIED?minimum:Math.max(minimum,MeasureSpec.getSize(widthSpec));
  int height=Math.max((int)(8*cellHeight),MeasureSpec.getSize(heightSpec));setMeasuredDimension(width,height);
 }
 @Override protected void onSizeChanged(int w,int h,int oldw,int oldh){super.onSizeChanged(w,h,oldw,oldh);host.resize(Math.max(20,Math.min(300,(int)(w/cellWidth))),Math.max(8,Math.min(150,(int)(h/cellHeight))));}
 public void acceptFrame(byte[] bytes){frame=decode(bytes);frameGeneration++;invalidate();}
 public long renderedGeneration(){return drawnGeneration;}
 public String renderedText(){return drawnText;}
 public int displayedColumns(){return frame==null?0:frame.cols;}
 private static int u16(ByteBuffer b){return Short.toUnsignedInt(b.getShort());}
 private static Frame decode(byte[] bytes){
  if(bytes.length<15||bytes.length>4*1024*1024)throw new IllegalArgumentException("屏幕帧长度无效");
  ByteBuffer b=ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN);if(b.getInt()!=0x31464f49)throw new IllegalArgumentException("屏幕帧格式无效");
  Frame f=new Frame();f.cols=u16(b);f.rows=u16(b);f.cursorX=u16(b)-1;f.cursorY=u16(b)-1;int visible=Byte.toUnsignedInt(b.get());f.cursorVisible=visible==1;int palettes=u16(b);
  if(f.cols<20||f.cols>300||f.rows<8||f.rows>150||palettes<1||palettes>45000||visible>1)throw new IllegalArgumentException("屏幕尺寸无效");
  f.styles=new Style[palettes];for(int i=0;i<palettes;i++){Style s=new Style();s.fg=b.getInt();s.bg=b.getInt();s.attr=b.getInt();s.underline=Byte.toUnsignedInt(b.get());s.underlineColor=b.getInt();if(s.underline>5)throw new IllegalArgumentException("下划线样式无效");f.styles[i]=s;}
  f.cells=new Cell[f.cols*f.rows];for(int i=0;i<f.cells.length;i++){Cell c=new Cell();c.palette=u16(b);c.width=Byte.toUnsignedInt(b.get());int length=b.getInt();if(c.palette>=palettes||c.width>2||length<0||length>65536||length>b.remaining())throw new IllegalArgumentException("屏幕字符无效");byte[] text=new byte[length];b.get(text);c.text=new String(text,StandardCharsets.UTF_8);f.cells[i]=c;}
  if(b.hasRemaining())throw new IllegalArgumentException("屏幕帧存在额外数据");
  for(int i=0;i<f.cells.length;i++){Cell c=f.cells[i];int x=i%f.cols;if(c.width==0&&(x==0||f.cells[i-1].width!=2||!c.text.isEmpty()))throw new IllegalArgumentException("宽字符续格无效");if(c.width==2&&(x+1>=f.cols||f.cells[i+1].width!=0))throw new IllegalArgumentException("宽字符边界无效");}
  return f;
 }
 private static int dim(int color){return Color.rgb((int)(Color.red(color)*.65),(int)(Color.green(color)*.65),(int)(Color.blue(color)*.65));}
 private int foreground(Style s){return (s.attr&4)!=0?s.bg:s.fg;}
 private int background(Style s){return (s.attr&4)!=0?s.fg:s.bg;}
 private void drawGlyph(Canvas canvas,Cell cell,Style style,float x,float y,boolean cursor){
  int fg=foreground(style),bg=background(style);if((style.attr&16)!=0)fg=dim(fg);if(cursor){int swap=fg;fg=bg;bg=swap;}
  int width=Math.max(1,cell.width);paint.setStyle(Paint.Style.FILL);paint.setPathEffect(null);paint.setColor(bg);canvas.drawRect(x,y,x+cellWidth*width,y+cellHeight,paint);
  if((style.attr&2)!=0&&!blink&&!cursor)return;
  paint.setTypeface((style.attr&1)!=0?((style.attr&32)!=0?boldItalic:bold):((style.attr&32)!=0?italic:regular));paint.setTextSize(textSize);paint.setColor(fg);
  canvas.save();canvas.clipRect(x,y,x+cellWidth*width,y+cellHeight);canvas.drawText(cell.text,x,y+baseline,paint);
  int underline=style.underline;if(underline==0&&(style.attr&8)!=0)underline=1;
  if(underline!=0){paint.setColor(style.underlineColor==0?fg:style.underlineColor);paint.setStrokeWidth(Math.max(1,cellHeight/15));float line=y+cellHeight-2;
   if(underline==3){Path path=new Path();path.moveTo(x,line);for(float at=0;at<cellWidth*width;at+=2)path.lineTo(x+at,line+((int)(at/2)%2==0?-1:1));paint.setStyle(Paint.Style.STROKE);canvas.drawPath(path,paint);paint.setStyle(Paint.Style.FILL);}
   else{if(underline==4)paint.setPathEffect(new DashPathEffect(new float[]{1,2},0));if(underline==5)paint.setPathEffect(new DashPathEffect(new float[]{4,2},0));canvas.drawLine(x,line,x+cellWidth*width,line,paint);if(underline==2)canvas.drawLine(x,line-2,x+cellWidth*width,line-2,paint);paint.setPathEffect(null);}
  }
  if((style.attr&64)!=0){paint.setColor(fg);paint.setStrokeWidth(1);canvas.drawLine(x,y+cellHeight*.5f,x+cellWidth*width,y+cellHeight*.5f,paint);}canvas.restore();
 }
 @Override protected void onDraw(Canvas canvas){super.onDraw(canvas);canvas.drawColor(Color.BLACK);Frame f=frame;if(f==null)return;StringBuilder text=new StringBuilder();
  for(int y=0;y<f.rows;y++){for(int x=0;x<f.cols;x++){Cell c=f.cells[y*f.cols+x];if(c.width==0)continue;drawGlyph(canvas,c,f.styles[c.palette],x*cellWidth,y*cellHeight,false);text.append(c.text);}text.append('\n');}
  if(f.cursorVisible&&blink&&f.cursorX>=0&&f.cursorX<f.cols&&f.cursorY>=0&&f.cursorY<f.rows){int x=f.cursorX;if(f.cells[f.cursorY*f.cols+x].width==0&&x>0)x--;Cell c=f.cells[f.cursorY*f.cols+x];drawGlyph(canvas,c,f.styles[c.palette],x*cellWidth,f.cursorY*cellHeight,true);}
  if(!composing.isEmpty()&&f.cursorX>=0&&f.cursorY>=0){float x=f.cursorX*cellWidth,y=f.cursorY*cellHeight;paint.setTypeface(regular);paint.setTextSize(textSize);float width=Math.max(cellWidth,paint.measureText(composing));paint.setColor(0xff264466);canvas.drawRect(x,y,x+width,y+cellHeight,paint);paint.setColor(Color.WHITE);canvas.drawText(composing,x,y+baseline,paint);canvas.drawLine(x,y+cellHeight-1,x+width,y+cellHeight-1,paint);}
  drawnText=text.toString();drawnGeneration=frameGeneration;
 }
 @Override protected void onAttachedToWindow(){super.onAttachedToWindow();postDelayed(blinkTick,500);}
 @Override protected void onDetachedFromWindow(){removeCallbacks(blinkTick);super.onDetachedFromWindow();}
 @Override protected void onWindowVisibilityChanged(int visibility){super.onWindowVisibilityChanged(visibility);removeCallbacks(blinkTick);if(visibility==VISIBLE)postDelayed(blinkTick,500);}
 @Override public boolean onTouchEvent(android.view.MotionEvent event){
  float x=event.getX(),y=event.getY();switch(event.getActionMasked()){
   case MotionEvent.ACTION_DOWN:touchX=x;touchY=lastY=y;dragged=false;return true;
   case MotionEvent.ACTION_MOVE:if(Math.abs(y-touchY)>cellHeight*2&&Math.abs(y-touchY)>Math.abs(x-touchX)){dragged=true;getParent().requestDisallowInterceptTouchEvent(true);if(Math.abs(y-lastY)>cellHeight*3){host.key(y<lastY?"PgDn":"PgUp",0,0);lastY=y;}}return true;
   case MotionEvent.ACTION_UP:if(!dragged&&Math.abs(x-touchX)<cellWidth&&Math.abs(y-touchY)<cellHeight){requestFocus();performClick();int cx=(int)(x/cellWidth),cy=(int)(y/cellHeight);host.mouse(cx,cy,true);host.mouse(cx,cy,false);}return true;
   case MotionEvent.ACTION_CANCEL:return true;default:return super.onTouchEvent(event);
  }
 }
 @Override public boolean performClick(){super.performClick();return true;}
 @Override public boolean onCheckIsTextEditor(){return true;}
 private void commitComposition(){if(!composing.isEmpty()){host.input(composing);composing="";invalidate();}}
 @Override public InputConnection onCreateInputConnection(EditorInfo info){
  info.inputType=InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_FLAG_MULTI_LINE|InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS;info.imeOptions=EditorInfo.IME_FLAG_NO_EXTRACT_UI|EditorInfo.IME_FLAG_NO_FULLSCREEN;info.initialSelStart=info.initialSelEnd=0;
  return new BaseInputConnection(this,true){
   private final Editable editable=new SpannableStringBuilder();
   @Override public Editable getEditable(){return editable;}
   @Override public boolean setComposingText(CharSequence text,int position){super.setComposingText(text,position);composing=text.toString();invalidate();return true;}
   @Override public boolean commitText(CharSequence text,int position){host.input(text.toString());composing="";editable.clear();invalidate();return true;}
   @Override public boolean finishComposingText(){commitComposition();editable.clear();return true;}
   @Override public boolean deleteSurroundingText(int before,int after){if(!composing.isEmpty()){super.deleteSurroundingText(before,after);composing=editable.toString();invalidate();return true;}for(int i=0;i<Math.min(before,64);i++)host.key("Backspace",0,0);for(int i=0;i<Math.min(after,64);i++)host.key("Delete",0,0);return true;}
   @Override public boolean deleteSurroundingTextInCodePoints(int before,int after){return deleteSurroundingText(before,after);}
   @Override public boolean performEditorAction(int action){if(!composing.isEmpty())commitComposition();else host.key("Enter",0,0);return true;}
   @Override public boolean sendKeyEvent(KeyEvent event){return TerminalCanvas.this.dispatchKeyEvent(event);}
  };
 }
 @Override public boolean onKeyDown(int code,KeyEvent event){
  if(code==KeyEvent.KEYCODE_ESCAPE&&!composing.isEmpty()){composing="";invalidate();return true;}
  int mods=(event.isShiftPressed()?1:0)|(event.isCtrlPressed()?2:0)|(event.isAltPressed()?4:0)|(event.isMetaPressed()?8:0);String name=null;
  switch(code){case KeyEvent.KEYCODE_ENTER:case KeyEvent.KEYCODE_NUMPAD_ENTER:name="Enter";break;case KeyEvent.KEYCODE_TAB:name="Tab";break;case KeyEvent.KEYCODE_ESCAPE:name="Escape";break;case KeyEvent.KEYCODE_DEL:name="Backspace";break;case KeyEvent.KEYCODE_FORWARD_DEL:name="Delete";break;case KeyEvent.KEYCODE_DPAD_UP:name="Up";break;case KeyEvent.KEYCODE_DPAD_DOWN:name="Down";break;case KeyEvent.KEYCODE_DPAD_LEFT:name="Left";break;case KeyEvent.KEYCODE_DPAD_RIGHT:name="Right";break;case KeyEvent.KEYCODE_MOVE_HOME:name="Home";break;case KeyEvent.KEYCODE_MOVE_END:name="End";break;case KeyEvent.KEYCODE_PAGE_UP:name="PgUp";break;case KeyEvent.KEYCODE_PAGE_DOWN:name="PgDn";break;}
  if(code>=KeyEvent.KEYCODE_F1&&code<=KeyEvent.KEYCODE_F12)name="F"+(code-KeyEvent.KEYCODE_F1+1);
  if(name!=null){host.key(name,0,mods);return true;}
  int rune=event.getUnicodeChar(event.getMetaState()&~(KeyEvent.META_CTRL_MASK|KeyEvent.META_META_MASK));if(rune>0&&(rune&KeyCharacterMap.COMBINING_ACCENT)==0){host.key("Rune",rune,mods);return true;}
  return super.onKeyDown(code,event);
 }
}
