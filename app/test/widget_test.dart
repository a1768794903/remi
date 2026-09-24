import 'package:flutter_test/flutter_test.dart';
import 'package:remi/main.dart';

void main() {
  testWidgets('shows the Remi wearable connection card', (tester) async {
    await tester.pumpWidget(const RemiApp());

    expect(find.text('Today'), findsOneWidget);
    expect(find.text('Remi Wearable'), findsOneWidget);
    expect(find.text('Connect'), findsOneWidget);
    expect(find.text('Status: disconnected'), findsOneWidget);
  });
}
