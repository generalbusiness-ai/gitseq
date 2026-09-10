package main
import("fmt";"strconv")
func main(){for _,s:=range []string{"003000","003300","65535","65536","999999999999999999999999999999"}{n,e:=strconv.ParseUint(s,10,16);fmt.Printf("%q value=%d error=%v\n",s,n,e)}}
