package io.github.liveyum.iotools;

import android.content.Context;
import android.text.InputType;
import android.view.View;
import android.widget.*;
import org.json.*;
import java.util.*;

/** An editable request draft. Raw field text is retained even when incomplete JSON. */
final class RequestEditor {
    private final NativeUi ui;
    private final JSONObject original, catalog;
    private final LinkedHashMap<String,View> fields=new LinkedHashMap<>();
    private final HashMap<String,String> types=new HashMap<>();
    private final ArrayList<String> actions=new ArrayList<>();
    private final EditText id,name,endpoint,timeout;
    private final Spinner action;
    final LinearLayout view;

    RequestEditor(Context context,JSONObject request,JSONObject protocol,JSONObject saved) {
        ui=new NativeUi(context);original=NativeUi.clone(request);catalog=protocol;
        view=ui.column();
        LinearLayout base=ui.card();view.addView(base);
        LinearLayout heading=ui.row();heading.addView(ui.title(protocol.optString("name",request.optString("protocol").toUpperCase()),19),new LinearLayout.LayoutParams(0,-2,1));heading.addView(ui.text("请求草稿",12,NativeUi.MUTED));base.addView(heading);
        id=baseField(base,"请求标识","唯一请求 ID",request.optString("id"),R.id.request_id,saved,"id");
        name=baseField(base,"名称","便于识别的名称",request.optString("name"),R.id.request_name,saved,"name");
        base.addView(ui.label("操作"));action=new Spinner(context);action.setId(R.id.request_action);action.setContentDescription("请求操作");
        JSONArray options=protocol.optJSONArray("actions");ArrayList<String> labels=new ArrayList<>();
        if(options!=null)for(int i=0;i<options.length();i++){JSONObject o=options.optJSONObject(i);if(o!=null){String value=o.optString("id");actions.add(value);labels.add(o.optString("name",value)+"  ·  "+value);}}
        String current=saved==null?request.optString("action"):saved.optString("action",request.optString("action"));
        if(!actions.contains(current)){actions.add(current);labels.add(current);}
        ArrayAdapter<String> adapter=new ArrayAdapter<>(context,android.R.layout.simple_spinner_item,labels);adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);action.setAdapter(adapter);action.setSelection(Math.max(0,actions.indexOf(current)));base.addView(action,new LinearLayout.LayoutParams(-1,ui.dp(52)));
        endpoint=baseField(base,"连接地址","例如 https://api.example.com",request.optString("endpoint"),R.id.request_endpoint,saved,"endpoint");
        endpoint.setInputType(InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_VARIATION_URI);
        timeout=baseField(base,"超时","例如 15s、5m",request.optString("timeout"),R.id.request_timeout,saved,"timeout");
        LinearLayout params=ui.card();params.addView(ui.title("请求参数",18));params.addView(ui.text("空白字段采用引擎默认值；执行前会校验并预览",12,NativeUi.MUTED));view.addView(params);
        JSONObject values=request.optJSONObject("params");if(values==null)values=new JSONObject();
        JSONArray definitions=protocol.optJSONArray("fields");Set<String> seen=new HashSet<>();
        LinearLayout common=ui.column(),more=ui.column();params.addView(common);
        final String core=" topic topics payload json body headers query node_id node_ids address count unit value values qos limit offset object_id method_id arguments value_type group subject connector ";
        if(definitions!=null)for(int i=0;i<definitions.length();i++){
            JSONObject d=definitions.optJSONObject(i);if(d==null)continue;String key=d.optString("key");if(key.isEmpty()||seen.contains(key))continue;seen.add(key);
            boolean visible=values.has(key)||(saved!=null&&saved.optJSONObject("fields")!=null&&saved.optJSONObject("fields").has(key))||core.contains(" "+key+" ");
            addField(visible?common:more,key,d.optString("label",key),d.optString("type","text"),d.optString("hint",""),values.opt(key),saved);
        }
        Iterator<String> keys=values.keys();while(keys.hasNext()){String key=keys.next();if(!seen.contains(key))addField(common,key,key,infer(values.opt(key)),"来自配置的参数",values.opt(key),saved);}
        if(more.getChildCount()>0){Button expand=ui.button("连接、安全与更多参数  ▾",false,()->{});params.addView(expand);more.setVisibility(View.GONE);params.addView(more);expand.setOnClickListener(v->{boolean open=more.getVisibility()!=View.VISIBLE;more.setVisibility(open?View.VISIBLE:View.GONE);expand.setText(open?"收起更多参数  ▴":"连接、安全与更多参数  ▾");});}
        if(fields.isEmpty())params.addView(ui.text("此操作无需额外参数，可直接预览",14,NativeUi.MUTED));
        Button extra=ui.button("＋ 添加参数",false,()->addParameter(context,common));params.addView(extra);
    }

    private EditText baseField(LinearLayout parent,String label,String hint,String value,int viewId,JSONObject saved,String key){parent.addView(ui.label(label));EditText e=ui.input(hint,saved==null?value:saved.optString(key,value),false);e.setId(viewId);e.setContentDescription(label);parent.addView(e);return e;}
    private String infer(Object v){return v instanceof JSONObject||v instanceof JSONArray?"json":v instanceof Boolean?"boolean":v instanceof Number?"number":"text";}
    private void addField(LinearLayout parent,String key,String label,String type,String hint,Object value,JSONObject saved){
        parent.addView(ui.label(label+(label.equals(key)?"":" · "+key)));types.put(key,type);
        JSONObject raw=saved==null?null:saved.optJSONObject("fields");String text=raw!=null&&raw.has(key)?raw.optString(key):value==null||value==JSONObject.NULL?"":NativeUi.pretty(value);
        if(type.equals("boolean")){
            Spinner input=new Spinner(ui.context);input.setTag("param:"+key);input.setContentDescription(label);ArrayAdapter<String> a=new ArrayAdapter<>(ui.context,android.R.layout.simple_spinner_item,new String[]{"默认（未设置）","是 / true","否 / false"});a.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);input.setAdapter(a);input.setSelection(text.equals("true")?1:text.equals("false")?2:0);parent.addView(input,new LinearLayout.LayoutParams(-1,ui.dp(48)));fields.put(key,input);
        }else{
            EditText input=ui.input(hint.isEmpty()?key:hint,text,type.equals("json")||key.equals("body")||key.equals("payload"));input.setTag("param:"+key);input.setContentDescription(label);
            if(type.equals("number"))input.setInputType(InputType.TYPE_CLASS_NUMBER|InputType.TYPE_NUMBER_FLAG_SIGNED|InputType.TYPE_NUMBER_FLAG_DECIMAL);
            if(type.equals("password"))input.setInputType(InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_VARIATION_PASSWORD);
            if(type.equals("json"))input.setTypeface(android.graphics.Typeface.MONOSPACE);
            parent.addView(input);fields.put(key,input);
        }
        if(!hint.isEmpty()&&type.equals("boolean"))parent.addView(ui.text(hint,12,NativeUi.MUTED));
    }
    private void addParameter(Context context,LinearLayout parent){LinearLayout form=ui.column();form.setPadding(ui.dp(20),0,ui.dp(20),0);EditText key=ui.input("参数名","",false);form.addView(key);Spinner type=new Spinner(context);String[] types={"text","number","boolean","json","password"};type.setAdapter(new ArrayAdapter<>(context,android.R.layout.simple_spinner_dropdown_item,types));form.addView(type);android.app.AlertDialog dialog=new android.app.AlertDialog.Builder(context).setTitle("添加请求参数").setView(form).setNegativeButton("取消",null).setPositiveButton("添加",null).create();dialog.setOnShowListener(d->dialog.getButton(-1).setOnClickListener(v->{String k=key.getText().toString().trim();if(k.isEmpty()||fields.containsKey(k)){key.setError(k.isEmpty()?"请输入参数名":"此参数已存在");return;}addField(parent,k,k,types[type.getSelectedItemPosition()],"",null,null);dialog.dismiss();}));dialog.show();}
    private String fieldText(View view){if(view instanceof Spinner){int selected=((Spinner)view).getSelectedItemPosition();return selected==1?"true":selected==2?"false":"";}return ((EditText)view).getText().toString();}
    JSONObject draft(){JSONObject raw=new JSONObject();for(Map.Entry<String,View> e:fields.entrySet())try{raw.put(e.getKey(),fieldText(e.getValue()));}catch(JSONException ignored){}return NativeUi.json("id",id.getText().toString(),"name",name.getText().toString(),"action",actions.get(action.getSelectedItemPosition()),"endpoint",endpoint.getText().toString(),"timeout",timeout.getText().toString(),"fields",raw);}
    JSONObject request() throws JSONException {
        JSONObject result=NativeUi.clone(original),raw=draft();String[] base={"id","name","action","endpoint","timeout"};for(String k:base)result.put(k,raw.getString(k));
        if(result.optString("id").trim().isEmpty()){id.setError("请求 ID 不能为空");throw new JSONException("请输入请求 ID");}
        if(result.optString("endpoint").trim().isEmpty()){endpoint.setError("连接地址不能为空");throw new JSONException("请输入连接地址");}
        JSONObject params=NativeUi.clone(NativeUi.object(original.opt("params")));
        for(Map.Entry<String,View> e:fields.entrySet()){
            String key=e.getKey(),s=fieldText(e.getValue()),type=types.get(key);
            if(s.trim().isEmpty()){params.remove(key);continue;}
            try{
                Object value=s;
                if(type.equals("boolean"))value=s.equals("true");
                else if(type.equals("json")){value=ExactJson.parse(s);}
                else if(type.equals("number")){if(!s.matches("[-+]?[0-9]+(\\.[0-9]+)?"))throw new NumberFormatException("请输入数值");value=s;}
                params.put(key,value);
            }catch(Exception error){if(e.getValue() instanceof EditText)((EditText)e.getValue()).setError("格式错误");throw new JSONException("参数 "+key+" 格式错误："+error.getMessage());}
        }
        result.put("params",params);return result;
    }
}
