package modupdater.lib;

public final class Ids {
    private Ids() {
    }

    public static boolean validId(String id) {
        return id != null && id.matches("^[0-9a-f]{32}$");
    }
}
