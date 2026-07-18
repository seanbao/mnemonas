import 'dart:io';

enum ServerTransportSecurity {
  secureHttps,
  loopbackHttp,
  privateNetworkHttp,
  insecurePublicHttp,
}

final class ServerEndpoint {
  ServerEndpoint._({required this.uri, required this.transportSecurity});

  final Uri uri;
  final ServerTransportSecurity transportSecurity;

  bool get isSecure => transportSecurity == ServerTransportSecurity.secureHttps;

  bool get isAllowedByDefault =>
      transportSecurity != ServerTransportSecurity.insecurePublicHttp;

  bool get usesLanHttp =>
      transportSecurity == ServerTransportSecurity.privateNetworkHttp;

  String get baseUrl => uri.toString();

  static ServerEndpoint parse(
    String input, {
    bool allowInsecurePublicHttp = false,
  }) {
    final value = input.trim();
    if (value.isEmpty) {
      throw const FormatException('Server URL is required');
    }

    final parsed = Uri.tryParse(value);
    if (parsed == null || !parsed.hasScheme || !parsed.hasAuthority) {
      throw const FormatException('Enter a complete HTTP or HTTPS URL');
    }

    final scheme = parsed.scheme.toLowerCase();
    if (scheme != 'http' && scheme != 'https') {
      throw const FormatException(
        'Only HTTP and HTTPS server URLs are allowed',
      );
    }
    if (parsed.host.isEmpty) {
      throw const FormatException('Server URL must include a host');
    }
    if (parsed.userInfo.isNotEmpty) {
      throw const FormatException('Server URL must not include credentials');
    }
    if (parsed.hasQuery) {
      throw const FormatException('Server URL must not include a query');
    }
    if (parsed.hasFragment) {
      throw const FormatException('Server URL must not include a fragment');
    }
    if (parsed.path.isNotEmpty && parsed.path != '/') {
      throw const FormatException('Server URL must not include a path');
    }

    // Accessing the port validates an explicitly supplied port.
    final port = parsed.hasPort ? parsed.port : null;
    if (port != null && (port < 1 || port > 65535)) {
      throw const FormatException('Server URL contains an invalid port');
    }
    final host = parsed.host.toLowerCase();
    final normalized = Uri(
      scheme: scheme,
      host: host,
      port: _isDefaultPort(scheme, port) ? null : port,
    );

    final security = _classifyTransport(scheme, host);
    if (security == ServerTransportSecurity.insecurePublicHttp &&
        !allowInsecurePublicHttp) {
      throw const FormatException(
        'Public servers require HTTPS; HTTP is limited to local networks',
      );
    }

    return ServerEndpoint._(uri: normalized, transportSecurity: security);
  }

  static bool _isDefaultPort(String scheme, int? port) {
    return (scheme == 'http' && port == 80) ||
        (scheme == 'https' && port == 443);
  }

  static ServerTransportSecurity _classifyTransport(
    String scheme,
    String host,
  ) {
    if (scheme == 'https') {
      return ServerTransportSecurity.secureHttps;
    }
    if (_isLoopbackHost(host)) {
      return ServerTransportSecurity.loopbackHttp;
    }
    if (_isPrivateNetworkHost(host)) {
      return ServerTransportSecurity.privateNetworkHttp;
    }
    return ServerTransportSecurity.insecurePublicHttp;
  }

  static bool _isLoopbackHost(String host) {
    if (host == 'localhost' || host.endsWith('.localhost')) {
      return true;
    }
    final address = InternetAddress.tryParse(host);
    return address?.isLoopback ?? false;
  }

  static bool _isPrivateNetworkHost(String host) {
    if (host.endsWith('.local') || !host.contains('.')) {
      return true;
    }

    final address = InternetAddress.tryParse(host);
    if (address == null) {
      return false;
    }
    if (address.type == InternetAddressType.IPv4) {
      final octets = address.rawAddress;
      return octets[0] == 10 ||
          (octets[0] == 172 && octets[1] >= 16 && octets[1] <= 31) ||
          (octets[0] == 192 && octets[1] == 168) ||
          (octets[0] == 169 && octets[1] == 254);
    }

    final bytes = address.rawAddress;
    final isUniqueLocal = (bytes[0] & 0xfe) == 0xfc;
    final isLinkLocal = bytes[0] == 0xfe && (bytes[1] & 0xc0) == 0x80;
    return isUniqueLocal || isLinkLocal;
  }

  @override
  bool operator ==(Object other) => other is ServerEndpoint && other.uri == uri;

  @override
  int get hashCode => uri.hashCode;

  @override
  String toString() => baseUrl;
}
