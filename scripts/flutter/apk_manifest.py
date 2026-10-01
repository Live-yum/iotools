"""Read attributes from the actual binary AndroidManifest, without an SDK runtime."""
import struct

def elements(data):
    def u32(offset):
        return struct.unpack_from('<I',data,offset)[0]
    def length(offset, width):
        if width==1:
            first=data[offset]
            return (((first&127)<<8)|data[offset+1],offset+2) if first&128 else (first,offset+1)
        first=struct.unpack_from('<H',data,offset)[0]
        return (((first&32767)<<16)|struct.unpack_from('<H',data,offset+2)[0],offset+4) if first&32768 else (first,offset+2)
    assert data[:2]==b'\x03\x00','Expected binary AndroidManifest'
    result=[];strings=[];offset=8
    while offset<len(data):
        kind,header,size=struct.unpack_from('<HHI',data,offset)
        assert size>=header and size>0 and offset+size<=len(data),'Invalid manifest chunk'
        if kind==1:
            count=u32(offset+8);utf8=bool(u32(offset+16)&256);base=offset+u32(offset+20)
            for i in range(count):
                pos=base+u32(offset+header+4*i);n,pos=length(pos,1 if utf8 else 2)
                if utf8:n,pos=length(pos,1)
                end=pos+n*(1 if utf8 else 2)
                assert end<=offset+size,'Invalid manifest string'
                strings.append(data[pos:end].decode('utf-8' if utf8 else 'utf-16-le'))
        elif kind==0x102:
            tag=strings[u32(offset+20)];start,width,count=struct.unpack_from('<HHH',data,offset+24);attrs={}
            assert width>=20
            for i in range(count):
                item=offset+16+start+i*width;assert item+20<=offset+size
                name=strings[u32(item+4)];raw=u32(item+8);typ=data[item+15];value=u32(item+16)
                attrs[name]=(strings[raw] if raw!=0xffffffff else strings[value]) if typ==3 else bool(value) if typ==0x12 else value
            result.append({'tag':tag,'attributes':attrs})
        offset+=size
    return result
