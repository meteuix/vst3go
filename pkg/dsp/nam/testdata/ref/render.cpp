// Reference renderer for pkg/dsp/nam tests: runs a .nam file through
// NeuralAmpModelerCore with fast tanh (as the NAM plugin does) on raw
// little-endian float32 mono input in fixed blocks.
//
// usage: render model.nam in.f32 out.f32 block [slimmable-size]
#include <algorithm>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <vector>

#include "NAM/activations.h"
#include "NAM/get_dsp.h"
#include "NAM/slimmable.h"

int main(int argc, char** argv)
{
  if (argc < 5)
    return 2;
  nam::activations::Activation::enable_fast_tanh();
  auto dsp = nam::get_dsp(std::filesystem::path(argv[1]));
  const int block = atoi(argv[4]);
  const double rate = dsp->GetExpectedSampleRate();
  dsp->Reset(rate > 0 ? rate : 48000, block);
  if (argc > 5)
  {
    auto* slim = dynamic_cast<nam::SlimmableModel*>(dsp.get());
    if (!slim)
      return 3;
    slim->SetSlimmableSize(atof(argv[5]));
  }
  if (dsp->NumInputChannels() != 1 || dsp->NumOutputChannels() != 1)
    return 4;

  std::ifstream f(argv[2], std::ios::binary | std::ios::ate);
  const auto size = f.tellg();
  f.seekg(0);
  std::vector<float> x(size / 4);
  f.read(reinterpret_cast<char*>(x.data()), size);
  std::vector<float> y(x.size());
  for (size_t i = 0; i < x.size(); i += block)
  {
    const int n = (int)std::min<size_t>(block, x.size() - i);
    float* ip = x.data() + i;
    float* op = y.data() + i;
    dsp->process(&ip, &op, n);
  }
  std::ofstream out(argv[3], std::ios::binary);
  out.write(reinterpret_cast<const char*>(y.data()), y.size() * 4);
  return 0;
}
