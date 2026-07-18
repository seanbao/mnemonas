import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/files/file_path.dart';

void main() {
  group('logical file paths', () {
    test('normalizes an absolute logical path', () {
      expect(
        normalizeLogicalPath('documents//report.pdf'),
        '/documents/report.pdf',
      );
      expect(normalizeLogicalPath('/'), '/');
    });

    test('encodes each segment while preserving separators', () {
      expect(
        encodeLogicalPath('/照片/家庭 相册/a#1.jpg'),
        '%E7%85%A7%E7%89%87/%E5%AE%B6%E5%BA%AD%20%E7%9B%B8%E5%86%8C/a%231.jpg',
      );
    });

    test('rejects dot segments, control characters, and forbidden root', () {
      expect(() => encodeLogicalPath('/docs/../secret'), throwsFormatException);
      expect(() => encodeLogicalPath('/docs/./file'), throwsFormatException);
      expect(() => encodeLogicalPath('/docs/\nfile'), throwsFormatException);
      expect(() => encodeLogicalPath(r'/docs\secret'), throwsFormatException);
      expect(
        () => encodeLogicalPath('/', allowRoot: false),
        throwsFormatException,
      );
    });

    test('validates a rename as exactly one logical segment', () {
      expect(validateLogicalName('家庭 相册'), '家庭 相册');
      for (final value in [
        '',
        '.',
        '..',
        'nested/name',
        r'nested\name',
        'bad\n',
      ]) {
        expect(
          () => validateLogicalName(value),
          throwsFormatException,
          reason: value,
        );
      }
    });

    test('creates a stable available name while preserving the extension', () {
      expect(
        uniqueLogicalName('report.pdf', {'report.pdf', 'report (1).pdf'}),
        'report (2).pdf',
      );
      expect(uniqueLogicalName('.env', {'.env'}), '.env (1)');
      expect(uniqueLogicalName('archive', {'archive'}), 'archive (1)');
      expect(uniqueLogicalName('photo.jpg', const {}), 'photo.jpg');
    });
  });
}
