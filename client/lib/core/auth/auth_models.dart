final class AuthUser {
  const AuthUser({
    required this.id,
    required this.username,
    required this.role,
    required this.homeDirectory,
    required this.mustChangePassword,
    this.email,
    this.groups = const [],
  });

  factory AuthUser.fromJson(Map<String, dynamic> json) {
    return AuthUser(
      id: _requiredString(json, 'id'),
      username: _requiredString(json, 'username'),
      email: _optionalString(json['email']),
      role: _requiredString(json, 'role'),
      groups: switch (json['groups']) {
        final List<dynamic> values => values.whereType<String>().toList(),
        _ => const [],
      },
      homeDirectory: _requiredString(json, 'home_dir'),
      mustChangePassword: json['must_change_password'] == true,
    );
  }

  final String id;
  final String username;
  final String? email;
  final String role;
  final List<String> groups;
  final String homeDirectory;
  final bool mustChangePassword;
}

final class AuthTokenPair {
  const AuthTokenPair({
    required this.accessToken,
    required this.refreshToken,
    required this.expiresAt,
    this.tokenType = 'Bearer',
  });

  factory AuthTokenPair.fromJson(Map<String, dynamic> json) {
    final expiresAtValue = _requiredString(json, 'expires_at');
    final expiresAt = DateTime.tryParse(expiresAtValue);
    if (expiresAt == null) {
      throw const FormatException('Invalid token expiry');
    }

    final tokenType = _optionalString(json['token_type']) ?? 'Bearer';
    if (tokenType.toLowerCase() != 'bearer') {
      throw const FormatException('Unsupported token type');
    }

    return AuthTokenPair(
      accessToken: _requiredString(json, 'access_token'),
      refreshToken: _requiredString(json, 'refresh_token'),
      expiresAt: expiresAt.toUtc(),
      tokenType: tokenType,
    );
  }

  final String accessToken;
  final String refreshToken;
  final DateTime expiresAt;
  final String tokenType;

  Map<String, dynamic> toJson() => {
    'access_token': accessToken,
    'refresh_token': refreshToken,
    'expires_at': expiresAt.toUtc().toIso8601String(),
    'token_type': tokenType,
  };
}

final class AuthSession {
  const AuthSession({
    required this.serverBaseUrl,
    required this.tokens,
    this.lastRefreshAt,
  });

  factory AuthSession.fromJson(Map<String, dynamic> json) {
    final schemaVersion = json['schema_version'];
    if (schemaVersion != 1) {
      throw const FormatException('Unsupported session schema');
    }
    final tokenJson = json['tokens'];
    if (tokenJson is! Map<String, dynamic>) {
      throw const FormatException('Invalid session tokens');
    }

    final lastRefreshValue = _optionalString(json['last_refresh_at']);
    final lastRefreshAt = lastRefreshValue == null
        ? null
        : DateTime.tryParse(lastRefreshValue);
    if (lastRefreshValue != null && lastRefreshAt == null) {
      throw const FormatException('Invalid refresh timestamp');
    }

    return AuthSession(
      serverBaseUrl: _requiredString(json, 'server_base_url'),
      tokens: AuthTokenPair.fromJson(tokenJson),
      lastRefreshAt: lastRefreshAt?.toUtc(),
    );
  }

  final String serverBaseUrl;
  final AuthTokenPair tokens;
  final DateTime? lastRefreshAt;

  AuthSession rotated(AuthTokenPair next, DateTime rotatedAt) => AuthSession(
    serverBaseUrl: serverBaseUrl,
    tokens: next,
    lastRefreshAt: rotatedAt.toUtc(),
  );

  Map<String, dynamic> toJson() => {
    'schema_version': 1,
    'server_base_url': serverBaseUrl,
    'tokens': tokens.toJson(),
    if (lastRefreshAt case final value?)
      'last_refresh_at': value.toUtc().toIso8601String(),
  };
}

final class LoginResult {
  const LoginResult({required this.user, required this.session});

  final AuthUser user;
  final AuthSession session;
}

String _requiredString(Map<String, dynamic> json, String key) {
  final value = _optionalString(json[key]);
  if (value == null) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

String? _optionalString(Object? value) {
  if (value is! String || value.trim().isEmpty) {
    return null;
  }
  return value;
}
