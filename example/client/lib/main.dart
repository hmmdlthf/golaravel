import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;

void main() {
  runApp(const GoLaravelInvoicingApp());
}

class GoLaravelInvoicingApp extends StatelessWidget {
  const GoLaravelInvoicingApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'GoLaravel SaaS Invoicing',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFFF43F5E), // Rose 500
          brightness: Brightness.dark,
          surface: const Color(0xFF0F172A), // Slate 900
        ),
        scaffoldBackgroundColor: const Color(0xFF090D16),
        useMaterial3: true,
        fontFamily: 'sans-serif',
      ),
      home: const InvoicesDashboardPage(),
    );
  }
}

class InvoicesDashboardPage extends StatefulWidget {
  const InvoicesDashboardPage({super.key});

  @override
  State<InvoicesDashboardPage> createState() => _InvoicesDashboardPageState();
}

class _InvoicesDashboardPageState extends State<InvoicesDashboardPage> {
  List<dynamic> invoices = [];
  bool isLoading = true;
  String? errorMessage;

  @override
  void initState() {
    super.initState();
    fetchInvoices();
  }

  Future<void> fetchInvoices() async {
    setState(() {
      isLoading = true;
      errorMessage = null;
    });

    try {
      final response = await http.get(Uri.parse('/api/invoices'));
      if (response.statusCode == 200) {
        setState(() {
          invoices = json.decode(response.body);
          isLoading = false;
        });
      } else {
        setState(() {
          errorMessage = 'Server returned status ${response.statusCode}';
          isLoading = false;
        });
      }
    } catch (e) {
      setState(() {
        errorMessage = 'Failed to load invoices: $e';
        isLoading = false;
      });
    }
  }

  Future<void> markAsPaid(int id) async {
    try {
      final response = await http.patch(Uri.parse('/api/invoices/$id/pay'));
      if (response.statusCode == 200) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Invoice marked as PAID!'),
            backgroundColor: Color(0xFF10B981),
          ),
        );
        fetchInvoices();
      }
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to update invoice: $e')),
      );
    }
  }

  void openCreateDialog() {
    showDialog(
      context: context,
      builder: (ctx) => CreateInvoiceDialog(onCreated: fetchInvoices),
    );
  }

  @override
  Widget build(BuildContext context) {
    double totalBilled = 0;
    double totalPaid = 0;
    double totalPending = 0;

    for (var inv in invoices) {
      double amt = (inv['amount'] as num?)?.toDouble() ?? 0;
      totalBilled += amt;
      if (inv['status'] == 'paid') {
        totalPaid += amt;
      } else {
        totalPending += amt;
      }
    }

    return Scaffold(
      appBar: AppBar(
        backgroundColor: const Color(0xFF0F172A),
        elevation: 0,
        title: Row(
          children: [
            const Text(
              'GoLaravel',
              style: TextStyle(fontWeight: FontWeight.w900, color: Color(0xFFF43F5E), fontSize: 22),
            ),
            const SizedBox(width: 8),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: const Color(0xFFF43F5E).withOpacity(0.15),
                borderRadius: BorderRadius.circular(12),
                border: Border.all(color: const Color(0xFFF43F5E)),
              ),
              child: const Text('Flutter Client', style: TextStyle(color: Color(0xFFFB7185), fontSize: 11, fontWeight: FontWeight.bold)),
            ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: 'Refresh Invoices',
            icon: const Icon(Icons.refresh),
            onPressed: fetchInvoices,
          ),
          const SizedBox(width: 8),
          FilledButton.icon(
            style: FilledButton.styleFrom(
              backgroundColor: const Color(0xFFE11D48),
              shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
            ),
            icon: const Icon(Icons.add, size: 18),
            label: const Text('New Invoice', style: TextStyle(fontWeight: FontWeight.bold)),
            onPressed: openCreateDialog,
          ),
          const SizedBox(width: 16),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: fetchInvoices,
        child: SingleChildScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.all(20.0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Hybrid Banner Explanation
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: const Color(0xFF1E293B).withOpacity(0.8),
                  borderRadius: BorderRadius.circular(16),
                  border: Border.all(color: const Color(0xFF334155)),
                ),
                child: Row(
                  children: [
                    const Icon(Icons.offline_bolt, color: Color(0xFFF43F5E), size: 36),
                    const SizedBox(width: 16),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: const [
                          Text('Hybrid Presentation Active', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 15, color: Colors.white)),
                          SizedBox(height: 4),
                          Text(
                            '• Interactive Dashboard: Powered by Flutter Multiplatform (Web, Windows, Mobile).\n'
                            '• Customer Public Invoices: Rendered in < 45ns via Templ SSR with zero client JS.',
                            style: TextStyle(color: Color(0xFF94A3B8), fontSize: 13),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),

              const SizedBox(height: 20),

              // KPI Metric Cards
              LayoutBuilder(
                builder: (context, constraints) {
                  int cols = constraints.maxWidth > 800 ? 3 : 1;
                  return GridView.count(
                    crossAxisCount: cols,
                    shrinkWrap: true,
                    physics: const NeverScrollableScrollPhysics(),
                    crossAxisSpacing: 16,
                    mainAxisSpacing: 16,
                    childAspectRatio: constraints.maxWidth > 800 ? 2.5 : 3.2,
                    children: [
                      _kpiCard('Total Revenue Billed', '\$${totalBilled.toStringAsFixed(2)}', Icons.account_balance_wallet, const Color(0xFF818CF8)),
                      _kpiCard('Collected (Paid)', '\$${totalPaid.toStringAsFixed(2)}', Icons.check_circle_outline, const Color(0xFF34D399)),
                      _kpiCard('Pending Collection', '\$${totalPending.toStringAsFixed(2)}', Icons.schedule, const Color(0xFFFBBF24)),
                    ],
                  );
                },
              ),

              const SizedBox(height: 28),

              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  const Text('Invoices from Ent ORM', style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold, color: Colors.white)),
                  Text('${invoices.length} Total', style: const TextStyle(color: Color(0xFF94A3B8), fontSize: 14)),
                ],
              ),
              const SizedBox(height: 12),

              if (isLoading)
                const Center(child: Padding(padding: EdgeInsets.all(40.0), child: CircularProgressIndicator()))
              else if (errorMessage != null)
                Center(child: Padding(padding: const EdgeInsets.all(32.0), child: Text(errorMessage!, style: const TextStyle(color: Colors.red))))
              else if (invoices.isEmpty)
                const Center(child: Padding(padding: EdgeInsets.all(40.0), child: Text('No invoices found.')))
              else
                ListView.separated(
                  shrinkWrap: true,
                  physics: const NeverScrollableScrollPhysics(),
                  itemCount: invoices.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 12),
                  itemBuilder: (ctx, i) {
                    final inv = invoices[i];
                    final bool isPaid = inv['status'] == 'paid';
                    final String token = inv['token'] ?? '';
                    final String publicUrl = '/invoice/$token';

                    return Card(
                      color: const Color(0xFF1E293B),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14),
                        side: const BorderSide(color: Color(0xFF334155)),
                      ),
                      child: Padding(
                        padding: const EdgeInsets.all(16.0),
                        child: Row(
                          children: [
                            Container(
                              padding: const EdgeInsets.all(10),
                              decoration: BoxDecoration(
                                color: (isPaid ? const Color(0xFF10B981) : const Color(0xFFF59E0B)).withOpacity(0.15),
                                borderRadius: BorderRadius.circular(10),
                              ),
                              child: Icon(
                                isPaid ? Icons.check : Icons.receipt_long,
                                color: isPaid ? const Color(0xFF34D399) : const Color(0xFFFBBF24),
                                size: 24,
                              ),
                            ),
                            const SizedBox(width: 16),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Row(
                                    children: [
                                      Text(
                                        inv['invoice_number'] ?? 'INV',
                                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16, color: Colors.white, fontFamily: 'monospace'),
                                      ),
                                      const SizedBox(width: 10),
                                      Container(
                                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                                        decoration: BoxDecoration(
                                          color: isPaid ? const Color(0xFF064E3B) : const Color(0xFF78350F),
                                          borderRadius: BorderRadius.circular(6),
                                        ),
                                        child: Text(
                                          (inv['status'] ?? '').toUpperCase(),
                                          style: TextStyle(
                                            color: isPaid ? const Color(0xFF6EE7B7) : const Color(0xFFFCD34D),
                                            fontSize: 10,
                                            fontWeight: FontWeight.w800,
                                          ),
                                        ),
                                      ),
                                    ],
                                  ),
                                  const SizedBox(height: 4),
                                  Text(
                                    '${inv['customer_name']} • ${inv['customer_email']}',
                                    style: const TextStyle(color: Color(0xFF94A3B8), fontSize: 13),
                                  ),
                                ],
                              ),
                            ),
                            Column(
                              crossAxisAlignment: CrossAxisAlignment.end,
                              children: [
                                Text(
                                  '\$${((inv['amount'] as num?)?.toDouble() ?? 0).toStringAsFixed(2)}',
                                  style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 18, color: Color(0xFFF43F5E), fontFamily: 'monospace'),
                                ),
                                const SizedBox(height: 6),
                                Row(
                                  children: [
                                    OutlinedButton.icon(
                                      style: OutlinedButton.styleFrom(
                                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                                        side: const BorderSide(color: Color(0xFF475569)),
                                      ),
                                      icon: const Icon(Icons.open_in_new, size: 14),
                                      label: const Text('Public Templ Page', style: TextStyle(fontSize: 11)),
                                      onPressed: () {
                                        Clipboard.setData(ClipboardData(text: publicUrl));
                                        ScaffoldMessenger.of(context).showSnackBar(
                                          SnackBar(
                                            content: Text('Copied URL: $publicUrl (Open in browser to see instant zero-JS Templ SSR!)'),
                                          ),
                                        );
                                      },
                                    ),
                                    if (!isPaid) ...[
                                      const SizedBox(width: 8),
                                      FilledButton(
                                        style: FilledButton.styleFrom(
                                          backgroundColor: const Color(0xFF10B981),
                                          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                                        ),
                                        child: const Text('Mark Paid', style: TextStyle(fontSize: 11, fontWeight: FontWeight.bold)),
                                        onPressed: () => markAsPaid(inv['id']),
                                      ),
                                    ],
                                  ],
                                ),
                              ],
                            ),
                          ],
                        ),
                      ),
                    );
                  },
                ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _kpiCard(String title, String value, IconData icon, Color color) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: const Color(0xFF1E293B),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: const Color(0xFF334155)),
      ),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: color.withOpacity(0.12),
              borderRadius: BorderRadius.circular(12),
            ),
            child: Icon(icon, color: color, size: 28),
          ),
          const SizedBox(width: 14),
          Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Text(title, style: const TextStyle(color: Color(0xFF94A3B8), fontSize: 13, fontWeight: FontWeight.w500)),
              const SizedBox(height: 4),
              Text(value, style: const TextStyle(color: Colors.white, fontSize: 20, fontWeight: FontWeight.bold, fontFamily: 'monospace')),
            ],
          ),
        ],
      ),
    );
  }
}

class CreateInvoiceDialog extends StatefulWidget {
  final VoidCallback onCreated;
  const CreateInvoiceDialog({super.key, required this.onCreated});

  @override
  State<CreateInvoiceDialog> createState() => _CreateInvoiceDialogState();
}

class _CreateInvoiceDialogState extends State<CreateInvoiceDialog> {
  final _customerNameCtrl = TextEditingController();
  final _customerEmailCtrl = TextEditingController();
  final _itemDescCtrl = TextEditingController(text: 'GoLaravel Cloud Production Node');
  final _itemQtyCtrl = TextEditingController(text: '1');
  final _itemPriceCtrl = TextEditingController(text: '1500.00');
  bool isSubmitting = false;

  Future<void> submit() async {
    if (_customerNameCtrl.text.isEmpty || _customerEmailCtrl.text.isEmpty) return;

    setState(() => isSubmitting = true);
    try {
      final payload = {
        'customer_name': _customerNameCtrl.text,
        'customer_email': _customerEmailCtrl.text,
        'due_date_days': 14,
        'items': [
          {
            'description': _itemDescCtrl.text,
            'quantity': int.tryParse(_itemQtyCtrl.text) ?? 1,
            'unit_price': double.tryParse(_itemPriceCtrl.text) ?? 0.0,
          }
        ],
      };

      final res = await http.post(
        Uri.parse('/api/invoices'),
        headers: {'Content-Type': 'application/json'},
        body: json.encode(payload),
      );

      if (res.statusCode == 201) {
        widget.onCreated();
        Navigator.of(context).pop();
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Error: ${res.body}')),
        );
      }
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to create: $e')),
      );
    } finally {
      setState(() => isSubmitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: const Color(0xFF0F172A),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
      title: const Text('Create New Invoice (Ent ORM)', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
      content: SizedBox(
        width: 440,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextField(
                controller: _customerNameCtrl,
                decoration: const InputDecoration(labelText: 'Customer Name', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _customerEmailCtrl,
                decoration: const InputDecoration(labelText: 'Customer Email', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 16),
              const Text('Line Item Details:', style: TextStyle(fontWeight: FontWeight.bold, color: Colors.white)),
              const SizedBox(height: 8),
              TextField(
                controller: _itemDescCtrl,
                decoration: const InputDecoration(labelText: 'Item Description', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _itemQtyCtrl,
                      keyboardType: TextInputType.number,
                      decoration: const InputDecoration(labelText: 'Quantity', border: OutlineInputBorder()),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: TextField(
                      controller: _itemPriceCtrl,
                      keyboardType: const TextInputType.numberWithOptions(decimal: true),
                      decoration: const InputDecoration(labelText: 'Unit Price (USD)', border: OutlineInputBorder()),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          child: const Text('Cancel'),
          onPressed: () => Navigator.of(context).pop(),
        ),
        FilledButton(
          style: FilledButton.styleFrom(backgroundColor: const Color(0xFFF43F5E)),
          onPressed: isSubmitting ? null : submit,
          child: isSubmitting ? const CircularProgressIndicator(color: Colors.white) : const Text('Create & Issue'),
        ),
      ],
    );
  }
}
