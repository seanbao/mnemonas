import 'package:shared_preferences/shared_preferences.dart';

import 'server_endpoint.dart';

abstract interface class ServerPreferencesStore {
  Future<ServerEndpoint?> load();

  Future<void> save(
    ServerEndpoint endpoint, {
    bool allowInsecurePublicHttp = false,
  });

  Future<void> clear();
}

final class ServerPreferences implements ServerPreferencesStore {
  ServerPreferences({SharedPreferencesAsync? preferences})
    : _preferences = preferences ?? SharedPreferencesAsync();

  static const _serverUrlKey = 'mnemonas.server.url.v1';
  static const _allowPublicHttpKey = 'mnemonas.server.allow_public_http.v1';

  final SharedPreferencesAsync _preferences;

  @override
  Future<ServerEndpoint?> load() async {
    final value = await _preferences.getString(_serverUrlKey);
    if (value == null || value.isEmpty) {
      return null;
    }
    try {
      final allowPublicHttp =
          await _preferences.getBool(_allowPublicHttpKey) ?? false;
      return ServerEndpoint.parse(
        value,
        allowInsecurePublicHttp: allowPublicHttp,
      );
    } on FormatException {
      await _preferences.remove(_serverUrlKey);
      return null;
    }
  }

  @override
  Future<void> save(
    ServerEndpoint endpoint, {
    bool allowInsecurePublicHttp = false,
  }) async {
    if (endpoint.transportSecurity ==
            ServerTransportSecurity.insecurePublicHttp &&
        !allowInsecurePublicHttp) {
      throw const FormatException(
        'Saving a public HTTP server requires explicit approval',
      );
    }
    await _preferences.setString(_serverUrlKey, endpoint.baseUrl);
    await _preferences.setBool(_allowPublicHttpKey, allowInsecurePublicHttp);
  }

  @override
  Future<void> clear() async {
    await _preferences.remove(_serverUrlKey);
    await _preferences.remove(_allowPublicHttpKey);
  }
}
