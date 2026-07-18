import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  group('ServerEndpoint', () {
    test('normalizes scheme, host, default port, and trailing slash', () {
      final endpoint = ServerEndpoint.parse('  HTTPS://Example.COM:443/  ');

      expect(endpoint.baseUrl, 'https://example.com');
      expect(endpoint.transportSecurity, ServerTransportSecurity.secureHttps);
      expect(endpoint.isSecure, isTrue);
    });

    test('classifies local HTTP without treating it as public', () {
      expect(
        ServerEndpoint.parse('http://localhost:8080').transportSecurity,
        ServerTransportSecurity.loopbackHttp,
      );
      expect(
        ServerEndpoint.parse('http://192.168.1.20').transportSecurity,
        ServerTransportSecurity.privateNetworkHttp,
      );
      expect(
        ServerEndpoint.parse('http://nas.local').transportSecurity,
        ServerTransportSecurity.privateNetworkHttp,
      );
      expect(
        ServerEndpoint.parse('http://[fd00::42]').transportSecurity,
        ServerTransportSecurity.privateNetworkHttp,
      );
    });

    test('rejects public HTTP unless explicitly allowed', () {
      expect(
        () => ServerEndpoint.parse('http://nas.example.com'),
        throwsFormatException,
      );

      final endpoint = ServerEndpoint.parse(
        'http://nas.example.com',
        allowInsecurePublicHttp: true,
      );
      expect(
        endpoint.transportSecurity,
        ServerTransportSecurity.insecurePublicHttp,
      );
      expect(endpoint.isSecure, isFalse);
      expect(endpoint.isAllowedByDefault, isFalse);
    });

    test('rejects URL components that change request authority or routing', () {
      for (final value in [
        'ftp://nas.example.com',
        'https://user:secret@nas.example.com',
        'https://nas.example.com?token=secret',
        'https://nas.example.com#fragment',
        'https://nas.example.com/proxy-prefix',
        'https://nas.example.com:65536',
        'nas.example.com',
      ]) {
        expect(
          () => ServerEndpoint.parse(value),
          throwsFormatException,
          reason: value,
        );
      }
    });
  });
}
