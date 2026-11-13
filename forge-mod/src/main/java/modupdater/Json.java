package modupdater;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

final class Json {
    private final String s;
    private int i;

    private Json(String s) {
        this.s = s;
    }

    static Object parse(String text) {
        Json p = new Json(text);
        Object value = p.value();
        p.skip();
        if (p.i != p.s.length()) {
            throw new IllegalArgumentException("trailing");
        }
        return value;
    }

    static String quote(String text) {
        StringBuilder out = new StringBuilder(text.length() + 2);
        out.append('"');
        for (int n = 0; n < text.length(); n++) {
            char c = text.charAt(n);
            switch (c) {
                case '"' -> out.append("\\\"");
                case '\\' -> out.append("\\\\");
                case '\n' -> out.append("\\n");
                case '\r' -> out.append("\\r");
                case '\t' -> out.append("\\t");
                default -> {
                    if (c < 0x20) {
                        out.append(String.format("\\u%04x", (int) c));
                    } else {
                        out.append(c);
                    }
                }
            }
        }
        out.append('"');
        return out.toString();
    }

    private Object value() {
        skip();
        if (i >= s.length()) {
            throw new IllegalArgumentException("eof");
        }
        char c = s.charAt(i);
        if (c == '{') {
            return object();
        }
        if (c == '[') {
            return array();
        }
        if (c == '"') {
            return string();
        }
        if (c == 't' || c == 'f') {
            return bool();
        }
        if (c == 'n') {
            literal("null");
            return null;
        }
        return number();
    }

    private Map<String, Object> object() {
        expect('{');
        Map<String, Object> map = new LinkedHashMap<>();
        skip();
        if (peek('}')) {
            i++;
            return map;
        }
        while (true) {
            skip();
            String key = string();
            skip();
            expect(':');
            map.put(key, value());
            skip();
            if (peek('}')) {
                i++;
                return map;
            }
            expect(',');
        }
    }

    private List<Object> array() {
        expect('[');
        List<Object> list = new ArrayList<>();
        skip();
        if (peek(']')) {
            i++;
            return list;
        }
        while (true) {
            list.add(value());
            skip();
            if (peek(']')) {
                i++;
                return list;
            }
            expect(',');
        }
    }

    private String string() {
        skip();
        if (i >= s.length() || s.charAt(i) != '"') {
            throw new IllegalArgumentException("string");
        }
        i++;
        StringBuilder out = new StringBuilder();
        while (i < s.length()) {
            char c = s.charAt(i++);
            if (c == '"') {
                return out.toString();
            }
            if (c == '\\') {
                if (i >= s.length()) {
                    throw new IllegalArgumentException("escape");
                }
                char e = s.charAt(i++);
                switch (e) {
                    case '"', '\\', '/' -> out.append(e);
                    case 'b' -> out.append('\b');
                    case 'f' -> out.append('\f');
                    case 'n' -> out.append('\n');
                    case 'r' -> out.append('\r');
                    case 't' -> out.append('\t');
                    case 'u' -> {
                        if (i + 4 > s.length()) {
                            throw new IllegalArgumentException("unicode");
                        }
                        int cp = Integer.parseInt(s.substring(i, i + 4), 16);
                        i += 4;
                        out.append((char) cp);
                    }
                    default -> throw new IllegalArgumentException("escape");
                }
            } else if (c < 0x20) {
                throw new IllegalArgumentException("control");
            } else {
                out.append(c);
            }
        }
        throw new IllegalArgumentException("string");
    }

    private Boolean bool() {
        if (s.startsWith("true", i)) {
            i += 4;
            return Boolean.TRUE;
        }
        if (s.startsWith("false", i)) {
            i += 5;
            return Boolean.FALSE;
        }
        throw new IllegalArgumentException("bool");
    }

    private Number number() {
        int start = i;
        if (s.charAt(i) == '-') {
            i++;
        }
        if (i >= s.length() || !digit(s.charAt(i))) {
            throw new IllegalArgumentException("number");
        }
        while (i < s.length() && digit(s.charAt(i))) {
            i++;
        }
        boolean real = false;
        if (i < s.length() && s.charAt(i) == '.') {
            real = true;
            i++;
            if (i >= s.length() || !digit(s.charAt(i))) {
                throw new IllegalArgumentException("number");
            }
            while (i < s.length() && digit(s.charAt(i))) {
                i++;
            }
        }
        if (i < s.length() && (s.charAt(i) == 'e' || s.charAt(i) == 'E')) {
            real = true;
            i++;
            if (i < s.length() && (s.charAt(i) == '+' || s.charAt(i) == '-')) {
                i++;
            }
            if (i >= s.length() || !digit(s.charAt(i))) {
                throw new IllegalArgumentException("number");
            }
            while (i < s.length() && digit(s.charAt(i))) {
                i++;
            }
        }
        String token = s.substring(start, i);
        if (real) {
            return Double.valueOf(token);
        }
        try {
            return Long.valueOf(token);
        } catch (NumberFormatException ex) {
            return Double.valueOf(token);
        }
    }

    private void literal(String lit) {
        if (!s.startsWith(lit, i)) {
            throw new IllegalArgumentException("literal");
        }
        i += lit.length();
    }

    private void expect(char c) {
        skip();
        if (i >= s.length() || s.charAt(i) != c) {
            throw new IllegalArgumentException("expected");
        }
        i++;
    }

    private boolean peek(char c) {
        return i < s.length() && s.charAt(i) == c;
    }

    private void skip() {
        while (i < s.length()) {
            char c = s.charAt(i);
            if (c == ' ' || c == '\n' || c == '\r' || c == '\t') {
                i++;
            } else {
                break;
            }
        }
    }

    private static boolean digit(char c) {
        return c >= '0' && c <= '9';
    }
}
