package io.github.liveyum.iotools;
import org.json.*;
import java.math.BigDecimal;
import java.math.BigInteger;
/** Strict JSON parsing without Android JSONTokener's lossy double fallback. */
final class ExactJson {
 private final String source; private int position;
 private ExactJson(String source){this.source=source;}
 static Object parse(String source)throws JSONException{ExactJson p=new ExactJson(source);Object v=p.value(0);p.space();if(p.position!=source.length())throw p.error("多余字符");return v;}
 static JSONObject object(String source)throws JSONException{Object v=parse(source);if(!(v instanceof JSONObject))throw new JSONException("需要 JSON 对象");return (JSONObject)v;}
 private JSONException error(String s){return new JSONException(s+"（位置 "+position+"）");}
 private void space(){while(position<source.length()&&" \r\n\t".indexOf(source.charAt(position))>=0)position++;}
 private char next()throws JSONException{if(position>=source.length())throw error("意外结束");return source.charAt(position++);}
 private Object value(int depth)throws JSONException{
  if(depth>64)throw error("JSON 层级超过限制");space();char c=next();if(c=='"')return string();
  if(c=='{'){JSONObject o=new JSONObject();space();if(position<source.length()&&source.charAt(position)=='}'){position++;return o;}while(true){space();if(next()!='"')throw error("对象键必须是字符串");String k=string();if(o.has(k))throw error("重复对象键");space();if(next()!=':')throw error("缺少冒号");o.put(k,value(depth+1));space();char d=next();if(d=='}')return o;if(d!=',')throw error("缺少逗号");}}
  if(c=='['){JSONArray a=new JSONArray();space();if(position<source.length()&&source.charAt(position)==']'){position++;return a;}while(true){a.put(value(depth+1));space();char d=next();if(d==']')return a;if(d!=',')throw error("缺少逗号");}}
  int start=position-1;while(position<source.length()&&",]} \r\n\t".indexOf(source.charAt(position))<0)position++;String t=source.substring(start,position);if(t.equals("true"))return true;if(t.equals("false"))return false;if(t.equals("null"))return JSONObject.NULL;
  if(!t.matches("-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?")||t.length()>10000)throw error("无效 JSON 值");try{if(t.indexOf('.')>=0||t.indexOf('e')>=0||t.indexOf('E')>=0)return new BigDecimal(t);return new BigInteger(t);}catch(NumberFormatException e){throw error("无效数值");}
 }
 private String string()throws JSONException{int start=position-1;boolean escaped=false;while(position<source.length()){char c=source.charAt(position++);if(c<32)throw error("字符串含控制字符");if(c=='"'&&!escaped)return (String)new JSONTokener(source.substring(start,position)).nextValue();if(c=='\\'&&!escaped)escaped=true;else escaped=false;}throw error("字符串缺少引号");}
}
