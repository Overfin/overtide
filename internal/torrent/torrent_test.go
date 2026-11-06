package torrent

import "testing"

func TestFiles(t *testing.T) {
	pack := "d8:announce3:url4:infod5:filesld6:lengthi10e4:pathl7:a b.mkveed6:lengthi20e4:pathl3:sub5:c.mkveee4:name4:Pack12:piece lengthi1e6:pieces0:ee"
	files, err := Files([]byte(pack))
	if err != nil || len(files) != 2 || files[0] != (File{"a b.mkv", 10}) || files[1] != (File{"sub/c.mkv", 20}) {
		t.Fatalf("%v, %v", files, err)
	}
	single := "d4:infod6:lengthi7e4:name5:a.mkvee"
	files, err = Files([]byte(single))
	if err != nil || len(files) != 1 || files[0] != (File{"a.mkv", 7}) {
		t.Fatalf("%v, %v", files, err)
	}
	for _, bad := range []string{"", "x", "i1e", "d4:infoi1ee", "d4:infodee", pack + "x", "d4:infod5:filesli1eeee", "99999:a", "l", "d", "i99999999999999999999e", string(make([]byte, 40))} {
		if _, err := Files([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	deep := ""
	for range 100000 {
		deep += "l"
	}
	if _, err := Files([]byte(deep)); err == nil {
		t.Error("too deep: no error")
	}
}

func FuzzFiles(f *testing.F) {
	f.Add([]byte("d4:infod6:lengthi7e4:name5:a.mkvee"))
	f.Fuzz(func(t *testing.T, data []byte) {
		Files(data)
	})
}
