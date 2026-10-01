package io.github.liveyum.iotools;

import android.app.AlertDialog;
import android.content.Context;
import android.graphics.Typeface;
import android.view.View;
import android.widget.*;
import org.json.*;
import java.util.*;

/** Protocol-aware result cards consume only real engine events. */
final class ResultViews {
    interface Actions { void prepare(String action,String key,Object value); void filterHTTP(String body); void exportText(String title,String text); }
    private final NativeUi ui;
    private final Actions actions;
    ResultViews(Context c,Actions a){ui=new NativeUi(c);actions=a;}

    View event(JSONObject event){
        String kind=event.optString("kind");Object raw=event.opt("data");JSONObject data=NativeUi.object(raw);
        LinearLayout card=ui.card();LinearLayout head=ui.row();String title=label(kind);
        TextView caption=ui.title(title,16);head.addView(caption,new LinearLayout.LayoutParams(0,-2,1));String time=event.optString("time");if(time.length()>19)time=time.substring(11,19);head.addView(ui.text(time,11,NativeUi.MUTED));card.addView(head);ui.gap(card,10);
        if(kind.equals("response")||kind.equals("transformed")){
            int status=data.optInt("status");TextView badge=ui.title("HTTP "+status,24);badge.setTextColor(status>=400?NativeUi.DANGER:NativeUi.ACCENT);card.addView(badge);
            if(data.has("headers"))details(card,"响应头",data.opt("headers"));Object body=data.opt("body");tree(card,body,0);
            LinearLayout controls=ui.row();ui.weighted(controls,ui.button("筛选响应",false,()->actions.filterHTTP(NativeUi.pretty(body))));ui.weighted(controls,ui.button("导出响应",false,()->actions.exportText("response.json",NativeUi.pretty(body))));ui.gap(card,12);card.addView(controls);
        }else if(kind.equals("message")||kind.equals("record")){
            card.addView(ui.title(data.optString("topic","消息"),18));
            if(kind.equals("record")){ui.pair(card,"分区 / 偏移",data.optString("partition")+" / "+data.optString("offset"));ui.pair(card,"消息键",data.optString("key"));tree(card,data.opt("value"),0);}
            else {ui.pair(card,"QoS / 保留",data.optString("qos")+" / "+(data.optBoolean("retained")?"是":"否"));ui.pair(card,"字节数",data.optString("bytes"));tree(card,data.has("payload_json")?data.opt("payload_json"):data.opt("payload"),0);details(card,"十六进制 / 原始内容",data);}
            String topic=data.optString("topic");if(!topic.isEmpty()){ui.gap(card,10);card.addView(ui.button("以此主题创建"+(kind.equals("record")?"消费":"订阅")+"草稿",false,()->actions.prepare(kind.equals("record")?"consume":"subscribe","topic",topic)));}
        }else if(kind.equals("reference")){
            String node=nodeID(data),name=first(data,"display_name","browse_name","DisplayName","BrowseName");card.addView(ui.title(name.isEmpty()?node:name,18));ui.pair(card,"节点",node);ui.pair(card,"类型",first(data,"node_class","NodeClass"));
            if(!node.isEmpty()){
                LinearLayout row=ui.row();ui.weighted(row,ui.button("浏览子节点",false,()->actions.prepare("browse","node_id",node)));ui.weighted(row,ui.button("读取",false,()->actions.prepare("read","node_id",node)));card.addView(row);ui.gap(card,8);LinearLayout row2=ui.row();ui.weighted(row2,ui.button("属性",false,()->actions.prepare("attributes","node_id",node)));ui.weighted(row2,ui.button("订阅",false,()->actions.prepare("subscribe","node_id",node)));ui.weighted(row2,ui.button("写入",false,()->actions.prepare("write","node_id",node)));card.addView(row2);
            }
            details(card,"引用详情",data);
        }else if(kind.equals("topics")&&raw instanceof JSONObject){
            ArrayList<String> keys=new ArrayList<>();Iterator<String> iterator=data.keys();while(iterator.hasNext())keys.add(iterator.next());Collections.sort(keys);
            for(String topic:keys){LinearLayout topicRow=ui.column();topicRow.setPadding(0,ui.dp(8),0,ui.dp(8));topicRow.addView(ui.title(topic,16));Object value=data.opt(topic);JSONObject detail=NativeUi.object(value);ui.pair(topicRow,"分区",String.valueOf(NativeUi.object(detail.opt("Partitions")).length()));LinearLayout controls=ui.row();ui.weighted(controls,ui.button("消费消息",false,()->actions.prepare("consume","topic",topic)));ui.weighted(controls,ui.button("主题配置",false,()->actions.prepare("topic-config","topic",topic)));topicRow.addView(controls);details(topicRow,"主题详情",value);card.addView(topicRow);}
        }else if(kind.equals("registers")&&raw instanceof JSONArray){
            JSONArray rows=(JSONArray)raw;ui.pair(card,"本次返回",rows.length()+" 个寄存器");HorizontalScrollView horizontal=new HorizontalScrollView(ui.context);LinearLayout table=ui.column();String[] columns={"address","u16","i16","hex","f32","ascii"};LinearLayout header=ui.row();for(String key:columns){TextView t=ui.title(key.equals("address")?"地址":key,12);t.setPadding(ui.dp(8),ui.dp(8),ui.dp(8),ui.dp(8));header.addView(t,new LinearLayout.LayoutParams(ui.dp(key.equals("f32")?118:80),-2));}header.setBackgroundColor(NativeUi.TINT);table.addView(header);
            for(int i=0;i<Math.min(rows.length(),200);i++){JSONObject value=rows.optJSONObject(i);if(value==null)continue;LinearLayout line=ui.row();for(String key:columns){TextView t=ui.selectable(value.optString(key,"·"));t.setPadding(ui.dp(8),ui.dp(10),ui.dp(8),ui.dp(10));line.addView(t,new LinearLayout.LayoutParams(ui.dp(key.equals("f32")?118:80),-2));}line.setOnClickListener(v->show("寄存器 "+value.optString("address"),value));table.addView(line);}horizontal.addView(table);card.addView(horizontal);card.addView(ui.text("点按行查看全部解码格式",12,NativeUi.MUTED));if(rows.length()>200)details(card,"查看完整数据",rows);
        }else if(kind.equals("endpoint")){
            card.addView(ui.title(data.optString("url"),16));ui.pair(card,"安全策略",data.optString("security_policy"));ui.pair(card,"消息模式",data.optString("security_mode"));ui.pair(card,"证书指纹",data.optString("certificate_sha256"));card.addView(ui.text("发现结果仅为服务端声明，请核对证书指纹后配置连接",12,NativeUi.MUTED));details(card,"端点详情",data);
        }else if(kind.equals("error")||kind.equals("failed")){
            caption.setTextColor(NativeUi.DANGER);TextView error=ui.text(errorText(raw),14,NativeUi.DANGER);error.setTextIsSelectable(true);card.addView(error);
        }else if(kind.equals("value")||kind.equals("notification")||kind.equals("attribute")){
            ui.pair(card,"节点",data.optString("node_id"));ui.pair(card,"状态",data.optString("status"));tree(card,data.has("value")?data.opt("value"):data,0);details(card,"数据详情",data);
        }else tree(card,raw,0);
        return card;
    }
    private String errorText(Object raw){JSONObject data=NativeUi.object(raw);return data.optString("error",data.optString("message",NativeUi.pretty(raw)));}
    private String first(JSONObject object,String... keys){for(String key:keys){Object v=object.opt(key);if(v instanceof JSONObject){JSONObject o=(JSONObject)v;String s=o.optString("Text",o.optString("Name",""));if(!s.isEmpty())return s;}else if(v!=null&&v!=JSONObject.NULL&&!String.valueOf(v).isEmpty())return String.valueOf(v);}return "";}
    private String nodeID(JSONObject data){String direct=first(data,"node_id","NodeID");if(!direct.isEmpty()&&!direct.startsWith("{"))return direct;JSONObject n=data.optJSONObject("NodeID");if(n!=null)return n.optString("NodeID",n.optString("node_id",""));return "";}
    private void tree(LinearLayout parent,Object raw,int depth){
        if(raw instanceof JSONObject){JSONObject o=(JSONObject)raw;Iterator<String> keys=o.keys();int count=0;while(keys.hasNext()&&count++<60){String key=keys.next();Object v=o.opt(key);if(v instanceof JSONObject||v instanceof JSONArray)details(parent,key,v);else ui.pair(parent,key,shortText(NativeUi.pretty(v),3000));}if(keys.hasNext())details(parent,"更多字段",raw);}
        else if(raw instanceof JSONArray){JSONArray a=(JSONArray)raw;if(a.length()==0)parent.addView(ui.text("空列表",14,NativeUi.MUTED));for(int i=0;i<Math.min(a.length(),30);i++){Object value=a.opt(i);if(value instanceof JSONObject||value instanceof JSONArray)details(parent,"第 "+(i+1)+" 项",value);else parent.addView(ui.selectable(NativeUi.pretty(value)));}if(a.length()>30)details(parent,"全部 "+a.length()+" 项",raw);}
        else {String value=NativeUi.pretty(raw);parent.addView(ui.selectable(shortText(value,12000)));if(value.length()>12000)details(parent,"查看完整内容",raw);}
    }
    void details(LinearLayout parent,String title,Object value){Button button=ui.button(title+"  ›",false,()->show(title,value));LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(-1,-2);p.topMargin=ui.dp(8);parent.addView(button,p);}
    void show(String title,Object value){LinearLayout content=ui.column();content.setPadding(ui.dp(20),ui.dp(8),ui.dp(20),ui.dp(20));TextView text=ui.selectable(NativeUi.pretty(value));content.addView(text);new AlertDialog.Builder(ui.context).setTitle(title).setView(ui.scroll(content)).setPositiveButton("关闭",null).setNeutralButton("导出",(d,w)->actions.exportText("result.json",NativeUi.pretty(value))).show();}
    private String shortText(String s,int max){return s.length()>max?s.substring(0,max)+"\n…":s;}
    private String label(String kind){switch(kind){case "response":return "HTTP 响应";case "transformed":return "转换结果";case "message":return "MQTT 消息";case "record":return "Kafka 消息";case "reference":return "OPC UA 节点";case "registers":return "寄存器数据";case "topics":return "Kafka 主题";case "endpoint":return "发现的端点";case "notification":return "订阅通知";case "value":return "读取值";case "attribute":return "节点属性";case "error":case "failed":return "执行错误";case "complete":case "completed":return "执行结束";case "cancelled":return "已取消";case "started":return "开始执行";default:return kind;}}
}
