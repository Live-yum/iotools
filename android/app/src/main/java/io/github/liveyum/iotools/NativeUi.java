package io.github.liveyum.iotools;

import android.content.Context;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.content.res.ColorStateList;
import android.view.Gravity;
import android.view.View;
import android.widget.*;
import org.json.*;

/** Small native view vocabulary. No renderer, web runtime or terminal dependency. */
final class NativeUi {
    static int INK=Color.rgb(25,43,52), MUTED=Color.rgb(101,119,128), ACCENT=Color.rgb(0,118,124);
    static int BG=Color.rgb(245,248,250), CARD=Color.WHITE, LINE=Color.rgb(222,232,236);
    static int TINT=Color.rgb(225,243,242), DANGER=Color.rgb(167,50,54);
    static boolean dark=true;
    static void theme(boolean enabled){dark=enabled;if(enabled){INK=Color.rgb(231,241,249);MUTED=Color.rgb(150,172,190);ACCENT=Color.rgb(0,207,237);BG=Color.rgb(7,18,29);CARD=Color.rgb(14,32,47);LINE=Color.rgb(37,63,81);TINT=Color.rgb(16,51,65);DANGER=Color.rgb(255,159,132);}else{INK=Color.rgb(25,43,52);MUTED=Color.rgb(101,119,128);ACCENT=Color.rgb(0,118,124);BG=Color.rgb(245,248,250);CARD=Color.WHITE;LINE=Color.rgb(222,232,236);TINT=Color.rgb(225,243,242);DANGER=Color.rgb(167,50,54);}}
    final Context context;
    NativeUi(Context c){context=c;}
    int dp(int n){return Math.round(context.getResources().getDisplayMetrics().density*n);}
    LinearLayout column(){LinearLayout v=new LinearLayout(context);v.setOrientation(LinearLayout.VERTICAL);return v;}
    LinearLayout row(){LinearLayout v=new LinearLayout(context);v.setOrientation(LinearLayout.HORIZONTAL);v.setGravity(Gravity.CENTER_VERTICAL);return v;}
    GradientDrawable shape(int color,int radius){GradientDrawable d=new GradientDrawable();d.setColor(color);d.setCornerRadius(dp(radius));return d;}
    GradientDrawable outline(int color,int radius){GradientDrawable d=shape(color,radius);d.setStroke(dp(1),LINE);return d;}
    TextView text(String s,int size,int color){TextView v=new TextView(context);v.setText(s);v.setTextSize(size);v.setTextColor(color);v.setFontFeatureSettings("kern");v.setLineSpacing(dp(2),1);return v;}
    TextView title(String s,int size){TextView v=text(s,size,INK);v.setTypeface(Typeface.create("sans-serif-medium",Typeface.NORMAL));return v;}
    TextView label(String s){TextView v=text(s,12,MUTED);v.setPadding(0,dp(12),0,dp(7));return v;}
    TextView selectable(String s){TextView v=text(s,13,INK);v.setTextIsSelectable(true);v.setTypeface(Typeface.MONOSPACE);return v;}
    LinearLayout card(){LinearLayout v=column();v.setPadding(dp(18),dp(16),dp(18),dp(16));v.setBackground(outline(CARD,18));LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(-1,-2);p.bottomMargin=dp(12);v.setLayoutParams(p);return v;}
    Button button(String s,boolean primary,Runnable action){Button b=new Button(context);b.setText(s);b.setTextSize(14);b.setAllCaps(false);b.setMinHeight(dp(48));b.setMinimumHeight(dp(48));b.setMinWidth(0);b.setMinimumWidth(0);b.setPadding(dp(16),dp(9),dp(16),dp(9));b.setTextColor(primary?(dark?BG:Color.WHITE):ACCENT);b.setTypeface(Typeface.create("sans-serif-medium",0));b.setStateListAnimator(null);b.setBackground(new RippleDrawable(ColorStateList.valueOf(0x22007880),shape(primary?ACCENT:TINT,12),null));b.setOnClickListener(v->action.run());return b;}
    Button chip(String s,boolean selected,Runnable action){Button b=button(s,selected,action);b.setTextSize(12);b.setMinHeight(dp(48));b.setMinimumHeight(dp(48));b.setPadding(dp(14),dp(5),dp(14),dp(5));LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(-2,dp(48));p.rightMargin=dp(8);b.setLayoutParams(p);return b;}
    EditText input(String hint,String value,boolean multi){EditText v=new EditText(context);v.setTextColor(INK);v.setHintTextColor(MUTED);v.setTextSize(15);v.setHint(hint);v.setText(value);v.setPadding(dp(13),dp(11),dp(13),dp(11));v.setBackground(outline(BG,10));v.setSingleLine(!multi);v.setInputType(android.text.InputType.TYPE_CLASS_TEXT|(multi?android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE:0)|android.text.InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);v.setGravity(Gravity.TOP|Gravity.START);v.setMinHeight(dp(48));if(multi)v.setMinLines(3);return v;}
    void gap(LinearLayout p,int height){View v=new View(context);p.addView(v,new LinearLayout.LayoutParams(1,dp(height)));}
    void weighted(LinearLayout row,View view){LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(0,-2,1);p.rightMargin=dp(7);row.addView(view,p);}
    ScrollView scroll(View child){ScrollView s=new ScrollView(context);s.setFillViewport(true);s.setClipToPadding(false);s.addView(child);return s;}
    HorizontalScrollView chips(View row){HorizontalScrollView s=new HorizontalScrollView(context);s.setHorizontalScrollBarEnabled(false);s.addView(row);return s;}
    void pair(LinearLayout p,String label,String value){LinearLayout r=row();TextView k=text(label,12,MUTED);TextView v=text(value,13,INK);v.setTextIsSelectable(true);r.addView(k,new LinearLayout.LayoutParams(dp(98),-2));r.addView(v,new LinearLayout.LayoutParams(0,-2,1));r.setPadding(0,dp(5),0,dp(5));p.addView(r);}
    static JSONObject object(Object value){return value instanceof JSONObject?(JSONObject)value:new JSONObject();}
    static JSONArray array(Object value){return value instanceof JSONArray?(JSONArray)value:new JSONArray();}
    static String pretty(Object value){try{if(value instanceof JSONObject)return ((JSONObject)value).toString(2);if(value instanceof JSONArray)return ((JSONArray)value).toString(2);}catch(JSONException ignored){}return value==null||value==JSONObject.NULL?"null":String.valueOf(value);}
    static JSONObject json(Object... args){JSONObject o=new JSONObject();try{for(int i=0;i<args.length;i+=2)o.put(String.valueOf(args[i]),args[i+1]);}catch(JSONException e){throw new IllegalArgumentException(e);}return o;}
    static JSONObject clone(JSONObject source){try{return ExactJson.object(source.toString());}catch(JSONException e){return new JSONObject();}}
}
