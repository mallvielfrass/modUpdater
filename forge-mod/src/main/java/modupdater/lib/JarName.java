package modupdater.lib;

public final class JarName {
    private JarName() {
    }

    public static boolean safeJar(String name) {
        if (name == null || name.length() < 5 || name.length() > 180 || name.contains("..")) {
            return false;
        }
        return name.matches("^[A-Za-z0-9(\\[][A-Za-z0-9._+()\\[\\] -]*\\.jar$");
    }
}
