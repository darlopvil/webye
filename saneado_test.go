package main

import "testing"

func TestSanitizedDescriptions(t *testing.T) {
	casos := []struct{ nombre, entrada, quiero string }{
		{"real game description",
			"<html>\r\n<body>\r\n<p>Tras desvelar un antiguo misterio, Lara Croft emprende un viaje.</p>\r\n</body>\r\n</html>",
			"<p>Tras desvelar un antiguo misterio, Lara Croft emprende un viaje.</p>"},
		{"real phone description, with its stray ul",
			"<p>El iPhone 17 Pro.</p> <ul> <h4>Características Clave</h4> <ul> <li>Apple Intelligence</li> </ul>",
			"<p>El iPhone 17 Pro.</p> <ul> <h4>Características Clave</h4> <ul> <li>Apple Intelligence</li> </ul></ul>"},
		{"script removed with its content", `<p>a</p><script>alert(1)</script><p>b</p>`, "<p>a</p><p>b</p>"},
		{"attributes never survive", `<p onclick="x()" style="color:red" class="c">a</p>`, "<p>a</p>"},
		{"links and images are dropped, their text kept", `<a href="javascript:x()">mira</a> <img src="https://tracker.example/p.gif">`, "mira"},
		{"stray closing tags cannot close the page", `</div></main><p>a</p></ul>`, "<p>a</p>"},
		{"unclosed tags are closed", `<ul><li>a<li>b`, "<ul><li>a<li>b</li></li></ul>"},
		{"bigger headings become h4", `<h1>a</h1><h2>b</h2>`, "<h4>a</h4><h4>b</h4>"},
		{"entities and bare symbols stay text", `<p>5 &lt; 6 &amp; 7 > 3 &quot;x&quot;</p>`, "<p>5 &lt; 6 &amp; 7 &gt; 3 &#34;x&#34;</p>"},
		{"comments removed", `<!-- <script>x</script> --><p>a</p>`, "<p>a</p>"},
		{"unclosed script becomes harmless text", `<script>alert(1)`, "alert(1)"},
		{"style removed with its content", `<style>body{display:none}</style><p>a</p>`, "<p>a</p>"},
	}
	for _, c := range casos {
		if got := string(sanear(c.entrada)); got != c.quiero {
			t.Errorf("%s:\n got  %q\n want %q", c.nombre, got, c.quiero)
		}
	}
}
